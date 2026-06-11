package invoice_service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pdcgo/event_source"
	"github.com/pdcgo/invoice_service/invoice_v2"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/schema/services/selling_iface/v1"
	"github.com/pdcgo/schema/services/warehouse_iface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

type ProjectConfig struct {
	ProjectID string `env:"GOOGLE_CLOUD_PROJECT"`
}

func (projectCfg *ProjectConfig) PubsubTopicPath(topic string) string {
	return fmt.Sprintf("projects/%s/topics/%s", projectCfg.ProjectID, topic)
}

func (projectCfg *ProjectConfig) PubsubSubscriberPath(sub string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", projectCfg.ProjectID, sub)
}

type InvoicePushHandler event_source.PushHandler

// NewInvoicePushHandler decodes pushed InvoiceEvents and dispatches them by type.
// The per-event balance logic is not implemented yet (skeleton).
func NewInvoicePushHandler(
	db *gorm.DB,
	projectCfg *ProjectConfig,
) InvoicePushHandler {

	return func(ctx context.Context, msg *event_source.PushRequest) error {
		var err error

		switch msg.Subscription {
		case projectCfg.PubsubSubscriberPath("invoice-selling-sub"):
			var event selling_iface.SellingEvent
			if err = protojson.Unmarshal(msg.Message.Data, &event); err != nil {
				return err
			}

			// One transaction per event so each event's balance work is self-contained
			// (and an unknown event opens no transaction at all).
			switch data := event.Data.(type) {
			case *selling_iface.SellingEvent_OrderCreated:
				oc := data.OrderCreated
				return db.Transaction(func(tx *gorm.DB) error {
					return postOrderBalances(tx, oc.OrderId, false, oc.TransactionTime.AsTime())
				})
			case *selling_iface.SellingEvent_OrderCanceled:
				oc := data.OrderCanceled
				return db.Transaction(func(tx *gorm.DB) error {
					return postOrderBalances(tx, oc.OrderId, true, oc.TransactionTime.AsTime())
				})
			}
		case projectCfg.PubsubSubscriberPath("invoice-stock-sub"):
			var event warehouse_iface.StockEvent
			if err = protojson.Unmarshal(msg.Message.Data, &event); err != nil {
				return err
			}

			switch data := event.Data.(type) {
			case *warehouse_iface.StockEvent_RestockAccepted:
				ra := data.RestockAccepted
				return db.Transaction(func(tx *gorm.DB) error {
					return postCodFeeBalance(tx, float64(ra.TransactionId), time.Now())
				})
			case *warehouse_iface.StockEvent_StockProblem:
				sp := data.StockProblem
				return db.Transaction(func(tx *gorm.DB) error {
					return postProblemStockBalance(tx, sp.TransactionId, time.Now())
				})

				// schema for foundback unsupproted
				// case *warehouse_iface.StockEvent_StockFoundBack:
				// 	debugtool.LogJson(data)
				// 	return errors.New("unimplemented")
			}

		}

		return nil
	}
}

type ProblemStock struct {
	TeamID      uint64
	Amount      float64
	ForTeamID   uint64
	ProductName string
}

// postProblemStockBalance posts a STOCK_PROBLEM double entry for each warehouse-side
// lost/broken item of a transaction: the warehouse (team_id) owes the product team
// (for_team_id) the value of the lost/broken stock. It is forward-only — recovery
// (StockFoundBack) is handled separately.
func postProblemStockBalance(tx *gorm.DB, transactionId uint64, now time.Time) error {
	items, err := getProblemStock(tx, transactionId)
	if err != nil {
		return err
	}
	for _, item := range items {
		// skip degenerate rows so a bad row isn't a poison message.
		if item.TeamID == 0 || item.ForTeamID == 0 || item.TeamID == item.ForTeamID || item.Amount <= 0 {
			continue
		}
		note := fmt.Sprintf("stock problem tx %d %s", transactionId, item.ProductName)
		// The warehouse (team_id) owes the product team (for_team_id): a RECEIVABLE on
		// the product-team side mirrors to a PAYABLE on the warehouse side.
		if err := invoice_v2.PostBalanceLog(
			tx, item.ForTeamID, item.TeamID,
			invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_STOCK_PROBLEM,
			item.Amount, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
			note, 0, now,
		); err != nil {
			return err
		}
	}
	return nil
}

// getProblemStock returns the warehouse-side problem items (lost/broken at the
// warehouse) of an inv transaction: the warehouse that holds the stock (team_id),
// the line value (amount), the product-owning team (for_team_id) and product name.
func getProblemStock(tx *gorm.DB, transactionId uint64) ([]*ProblemStock, error) {
	items := []*ProblemStock{}
	err := tx.
		Table("inv_item_problems iip").
		Joins("left join inv_transactions it on iip.tx_id = it.id").
		Joins("left join inv_tx_items iti on iti.id = iip.tx_item_id").
		Joins("left join skus s on s.id = iti.sku_id").
		Joins("left join products p on p.id = s.product_id").
		Where("iip.problem_type IN ?", []string{"lost_w", "broken_w"}).
		Where("iip.tx_id = ?", transactionId).
		Select([]string{
			"it.warehouse_id as team_id",
			"iti.total as amount",
			"s.team_id as for_team_id",
			"p.name as product_name",
		}).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

type CodFee struct {
	CodFee    float64
	TeamID    uint64
	ForTeamID uint64
}

// postCodFeeBalance posts the COD_FEE double entry for an accepted restock: the
// transaction's team (team_id) owes the warehouse team (for_team_id = warehouse_id)
// the COD fee. It is forward-only — restock acceptances aren't reversed here.
func postCodFeeBalance(tx *gorm.DB, transactionId float64, now time.Time) error {
	info, err := getCodFee(tx, transactionId)
	if err != nil {
		return err
	}
	// skip degenerate rows (no fee, no warehouse, self-pair) so a bad row isn't a poison message.
	if info.TeamID == 0 || info.ForTeamID == 0 || info.TeamID == info.ForTeamID || info.CodFee <= 0 {
		return nil
	}
	note := fmt.Sprintf("restock %d cod fee", uint64(transactionId))
	// The warehouse (for_team_id) is owed by the team (team_id): a RECEIVABLE on the
	// warehouse side mirrors to a PAYABLE on the team side.
	return invoice_v2.PostBalanceLog(
		tx, info.ForTeamID, info.TeamID,
		invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_COD_FEE,
		info.CodFee, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
		note, 0, now,
	)
}

// getCodFee returns the COD fee charged on an inv transaction together with the
// team that pays it (the transaction's team) and the warehouse team that charges
// it (warehouse_id, used as a team id). Zero values (e.g. no restock cost row)
// signal "nothing to post".
func getCodFee(tx *gorm.DB, transactionId float64) (*CodFee, error) {
	info := CodFee{}
	err := tx.
		Table("inv_transactions it").
		Joins("left join restock_costs rc on rc.inv_transaction_id = it.id").
		Where("it.id = ?", transactionId).
		Select([]string{
			"rc.cod_fee",
			"it.warehouse_id as for_team_id",
			"it.team_id",
		}).
		Find(&info).
		Error
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// postOrderBalances posts (or reverses, when reverse=true) every balance entry an
// order produces — the cross-team product fees and the warehouse fee — within the
// caller's transaction so they commit or roll back together.
func postOrderBalances(tx *gorm.DB, orderID uint64, reverse bool, now time.Time) error {
	if err := postCrossOrderBalance(tx, orderID, reverse, now); err != nil {
		return err
	}
	return postWarehouseFeeBalance(tx, orderID, reverse, now)
}

// postCrossOrderBalance posts (or reverses) the PRODUCT_FEE double entry for an
// order's cross items: order team A owes product team B for the cross-sold line.
// reverse=true undoes it (swaps the pair and the balance type), so create+cancel
// for the same order nets to zero.
func postCrossOrderBalance(tx *gorm.DB, orderID uint64, reverse bool, now time.Time) error {
	items, err := getProductCrossItem(tx, orderID)
	if err != nil {
		return err
	}
	for _, item := range items {
		// skip degenerate rows (e.g. null product->team join) so a bad row isn't a poison message.
		if item.TeamID == 0 || item.ProductTeamID == 0 || item.TeamID == item.ProductTeamID || item.Total <= 0 {
			continue
		}
		team, forTeam := item.ProductTeamID, item.TeamID // B owed by A
		bt := invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
		verb := ""
		if reverse {
			team, forTeam = item.TeamID, item.ProductTeamID
			bt = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
			verb = "cancel "
		}
		note := fmt.Sprintf("%sorder %s cross product %s", verb, item.OrderExternalID, item.ProductName)
		if err := invoice_v2.PostBalanceLog(
			tx, team, forTeam,
			invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE,
			item.Total, bt, note, item.CreatedByID, now,
		); err != nil {
			return err
		}
	}
	return nil
}

type InvoicePushHttpHandler http.HandlerFunc

func NewInvoicePushHttpHandler(handler InvoicePushHandler) InvoicePushHttpHandler {
	return InvoicePushHttpHandler(event_source.NewMuxPushhandler(event_source.PushHandler(handler)))
}

type ProductCrossItem struct {
	TeamID          uint64
	ProductTeamID   uint64
	CreatedByID     uint64
	OrderExternalID string
	ProductName     string
	Count           int
	Total           float64
}

// getProductCrossItem returns the cross-team items of an order (items not owned
// by the ordering team), with the ordering team, the product-owning team, and
// the line totals.
func getProductCrossItem(tx *gorm.DB, orderID uint64) ([]*ProductCrossItem, error) {
	items := []*ProductCrossItem{}
	err := tx.
		Table("order_items oi").
		Joins("left join orders o on o.id = oi.order_id").
		Joins("left join products p on p.id = oi.product_id").
		Where("oi.owned != ?", true).
		Where("oi.order_id = ?", orderID).
		Select([]string{
			"o.team_id",
			"o.order_ref_id as order_external_id",
			"p.team_id as product_team_id",
			"oi.count",
			"oi.total",
			"oi.product_name",
			"o.created_by_id",
		}).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// postWarehouseFeeBalance posts (or reverses) the WAREHOUSE_FEE double entry for
// an order: the ordering team A owes the warehouse team B the order's warehouse
// fee. reverse=true undoes it (swaps the pair and the balance type), so
// create+cancel for the same order nets to zero.
func postWarehouseFeeBalance(tx *gorm.DB, orderID uint64, reverse bool, now time.Time) error {
	info, err := getWarehouseFee(tx, orderID)
	if err != nil {
		return err
	}
	// skip degenerate rows (no warehouse, zero fee, etc.) so a bad row isn't a poison message.
	if info.TeamID == 0 || info.WarehouseID == 0 || info.TeamID == info.WarehouseID || info.Fee <= 0 {
		return nil
	}
	team, forTeam := info.WarehouseID, info.TeamID // B owed by A
	bt := invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
	verb := ""
	if reverse {
		team, forTeam = info.TeamID, info.WarehouseID
		bt = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
		verb = "cancel "
	}
	note := fmt.Sprintf("%sorder %s warehouse fee", verb, info.OrderExternalID)
	return invoice_v2.PostBalanceLog(
		tx, team, forTeam,
		invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE,
		info.Fee, bt, note, info.CreatedByID, now,
	)
}

type OrderWarehouseFee struct {
	TeamID          uint64
	WarehouseID     uint64
	CreatedByID     uint64
	OrderExternalID string
	Fee             float64
}

// getWarehouseFee returns the ordering team, the warehouse team that charged the
// fee (the inv transaction's warehouse_id, used as a team id), and the fee amount
// for an order. Zero values (e.g. no inv transaction) signal "nothing to post".
func getWarehouseFee(tx *gorm.DB, orderID uint64) (*OrderWarehouseFee, error) {
	info := OrderWarehouseFee{}
	err := tx.
		Table("orders o").
		Joins("left join inv_transactions it on it.id = o.invertory_tx_id").
		Where("o.id = ?", orderID).
		Select([]string{
			"o.team_id",
			"it.warehouse_id",
			"o.created_by_id",
			"o.order_ref_id as order_external_id",
			"o.warehouse_fee as fee",
		}).
		Find(&info).
		Error
	if err != nil {
		return nil, err
	}
	return &info, nil
}
