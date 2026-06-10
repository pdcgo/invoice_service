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
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

type InvoicePushHandler event_source.PushHandler

// NewInvoicePushHandler decodes pushed InvoiceEvents and dispatches them by type.
// The per-event balance logic is not implemented yet (skeleton).
func NewInvoicePushHandler(
	db *gorm.DB,
) InvoicePushHandler {

	return func(ctx context.Context, msg *event_source.PushRequest) error {

		var event selling_iface.SellingEvent
		if err := protojson.Unmarshal(msg.Message.Data, &event); err != nil {
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
		return nil
	}
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
