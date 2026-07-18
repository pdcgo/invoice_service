package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

func (s *invoiceServiceImpl) GetBalanceChangeSource(
	ctx context.Context,
	req *connect.Request[invoice_iface.GetBalanceChangeSourceRequest],
) (*connect.Response[invoice_iface.GetBalanceChangeSourceResponse], error) {
	db := s.db.WithContext(ctx)
	teamID := req.Msg.GetTeamId()
	result := &invoice_iface.GetBalanceChangeSourceResponse{}

	switch src := req.Msg.GetSourceType().(type) {
	case *invoice_iface.GetBalanceChangeSourceRequest_Order:
		rows := []invoice_models.BalanceChangeOrderSource{}
		query := teamScopedSource(db.Model(&invoice_models.BalanceChangeOrderSource{}), teamID).
			Where("order_id = ?", src.Order.GetOrderId())

		if src.Order.GetOrderSystem() != invoice_iface.OrderSystem_ORDER_SYSTEM_UNSPECIFIED {
			query = query.Where("order_system = ?", src.Order.GetOrderSystem())
		}
		err := query.
			Order("balance_change_log_id ASC").
			Find(&rows).
			Error
		if err != nil {
			return nil, err
		}

		items := make([]*invoice_iface.BalanceChangeOrderSourceItem, 0, len(rows))
		for i := range rows {
			items = append(items, toOrderSourceItem(&rows[i]))
		}
		result.Data = &invoice_iface.GetBalanceChangeSourceResponse_Order{
			Order: &invoice_iface.BalanceChangeOrderSourceData{Items: items},
		}

	case *invoice_iface.GetBalanceChangeSourceRequest_Restock:
		rows := []invoice_models.BalanceChangeRestockSource{}
		err := teamScopedSource(db.Model(&invoice_models.BalanceChangeRestockSource{}), teamID).
			Where("tx_id = ?", src.Restock.GetTxId()).
			Order("balance_change_log_id ASC").
			Find(&rows).
			Error
		if err != nil {
			return nil, err
		}

		items := make([]*invoice_iface.BalanceChangeRestockSourceItem, 0, len(rows))
		for i := range rows {
			items = append(items, toRestockSourceItem(&rows[i]))
		}
		result.Data = &invoice_iface.GetBalanceChangeSourceResponse_Restock{
			Restock: &invoice_iface.BalanceChangeRestockSourceData{Items: items},
		}

	case *invoice_iface.GetBalanceChangeSourceRequest_Broken:
		rows := []invoice_models.BalanceChangeBrokenSource{}
		err := teamScopedSource(db.Model(&invoice_models.BalanceChangeBrokenSource{}), teamID).
			Where("tx_id = ?", src.Broken.GetTxId()).
			Order("balance_change_log_id ASC").
			Find(&rows).
			Error
		if err != nil {
			return nil, err
		}

		items := make([]*invoice_iface.BalanceChangeBrokenSourceItem, 0, len(rows))
		for i := range rows {
			items = append(items, toBrokenSourceItem(&rows[i]))
		}
		result.Data = &invoice_iface.GetBalanceChangeSourceResponse_Broken{
			Broken: &invoice_iface.BalanceChangeBrokenSourceData{Items: items},
		}

	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source_type is required"))
	}

	return connect.NewResponse(result), nil
}

func teamScopedSource(q *gorm.DB, teamID uint64) *gorm.DB {
	return q.Where(
		"balance_change_log_id IN (?)",
		q.Session(&gorm.Session{NewDB: true}).
			Model(&invoice_models.BalanceChangeLog{}).
			Where("team_id = ?", teamID).
			Select("id"),
	)
}

func toOrderSourceItem(row *invoice_models.BalanceChangeOrderSource) *invoice_iface.BalanceChangeOrderSourceItem {
	return &invoice_iface.BalanceChangeOrderSourceItem{
		BalanceChangeLogId: row.BalanceChangeLogID,
		OrderSystem:        row.OrderSystem,
		OrderId:            row.OrderID,
		OrderItemId:        row.OrderItemID,
		TeamId:             row.TeamID,
		WarehouseId:        row.WarehouseID,
		CreatedAt:          timestamppb.New(row.CreatedAt),
	}
}

func toRestockSourceItem(row *invoice_models.BalanceChangeRestockSource) *invoice_iface.BalanceChangeRestockSourceItem {
	return &invoice_iface.BalanceChangeRestockSourceItem{
		BalanceChangeLogId: row.BalanceChangeLogID,
		TxId:               row.TxID,
		TxItemId:           row.TxItemID,
		TeamId:             row.TeamID,
		WarehouseId:        row.WarehouseID,
		CreatedAt:          timestamppb.New(row.CreatedAt),
	}
}

func toBrokenSourceItem(row *invoice_models.BalanceChangeBrokenSource) *invoice_iface.BalanceChangeBrokenSourceItem {
	return &invoice_iface.BalanceChangeBrokenSourceItem{
		BalanceChangeLogId: row.BalanceChangeLogID,
		TxId:               row.TxID,
		TxItemId:           row.TxItemID,
		TeamId:             row.TeamID,
		WarehouseId:        row.WarehouseID,
		CreatedAt:          timestamppb.New(row.CreatedAt),
	}
}
