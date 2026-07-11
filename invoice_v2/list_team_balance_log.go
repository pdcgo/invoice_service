package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/shared/db_connect"
	"gorm.io/gorm"
)

// ListTeamBalanceLog implements [invoice_ifaceconnect.InvoiceServiceHandler]. It
// lists the immutable balance change log of the scoped team (team_id), newest
// first, optionally filtered by counterparty, balance_type, and a created_at time
// window. Results are paginated.
func (s *invoiceServiceImpl) ListTeamBalanceLog(
	ctx context.Context,
	req *connect.Request[invoice_iface.ListTeamBalanceLogRequest],
) (*connect.Response[invoice_iface.ListTeamBalanceLogResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &invoice_iface.ListTeamBalanceLogResponse{
		Logs:     []*invoice_iface.BalanceChangeLog{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	// LEFT JOIN the order-source table so each row surfaces its order attribution
	// (0 / UNSPECIFIED when not order-sourced), and the optional order/warehouse/
	// order_system filters can restrict to a specific order. Predicates on the log
	// table are qualified (bcl.) because balance_change_order_sources also has team_id.
	var rows []balanceLogRow
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		query := db.
			Table("balance_change_logs bcl").
			Joins("LEFT JOIN balance_change_order_sources s ON s.balance_change_log_id = bcl.id").
			Select("bcl.*, COALESCE(s.order_id, 0) as order_id, COALESCE(s.warehouse_id, 0) as warehouse_id, COALESCE(s.order_system, 0) as order_system").
			Scopes(func(d *gorm.DB) *gorm.DB {
				d = d.Where("bcl.team_id = ?", pay.TeamId)
				if pay.ForTeamId > 0 {
					d = d.Where("bcl.for_team_id = ?", pay.ForTeamId)
				}
				if pay.BalanceType != invoice_iface.BalanceType_BALANCE_TYPE_UNSPECIFIED {
					d = d.Where("bcl.balance_type = ?", pay.BalanceType)
				}
				if pay.FromTime != nil {
					d = d.Where("bcl.created_at >= ?", pay.FromTime.AsTime())
				}
				if pay.ToTime != nil {
					d = d.Where("bcl.created_at <= ?", pay.ToTime.AsTime())
				}
				if pay.OrderId > 0 {
					d = d.Where("s.order_id = ?", pay.OrderId)
				}
				if pay.WarehouseId > 0 {
					d = d.Where("s.warehouse_id = ?", pay.WarehouseId)
				}
				if pay.OrderSystem != invoice_iface.OrderSystem_ORDER_SYSTEM_UNSPECIFIED {
					d = d.Where("s.order_system = ?", pay.OrderSystem)
				}
				return d
			})
		return query, nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	if err := paginated.Order("bcl.id DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for i := range rows {
		proto := toProtoBalanceChangeLog(&rows[i].BalanceChangeLog)
		proto.OrderId = rows[i].OrderID
		proto.WarehouseId = rows[i].WarehouseID
		proto.OrderSystem = rows[i].OrderSystem
		result.Logs = append(result.Logs, proto)
	}

	return connect.NewResponse(result), nil
}

// balanceLogRow scans a BalanceChangeLog plus its (LEFT-joined) order-source
// columns; the source columns are 0 / UNSPECIFIED for non-order-sourced rows.
type balanceLogRow struct {
	invoice_models.BalanceChangeLog
	OrderID     uint64
	WarehouseID uint64
	OrderSystem invoice_iface.OrderSystem
}
