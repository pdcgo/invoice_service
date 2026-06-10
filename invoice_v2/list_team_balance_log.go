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

	var rows []*invoice_models.BalanceChangeLog
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		query := db.
			Model(&invoice_models.BalanceChangeLog{}).
			Scopes(func(d *gorm.DB) *gorm.DB {
				d = d.Where("team_id = ?", pay.TeamId)
				if pay.ForTeamId > 0 {
					d = d.Where("for_team_id = ?", pay.ForTeamId)
				}
				if pay.BalanceType != invoice_iface.BalanceType_BALANCE_TYPE_UNSPECIFIED {
					d = d.Where("balance_type = ?", pay.BalanceType)
				}
				if pay.FromTime != nil {
					d = d.Where("created_at >= ?", pay.FromTime.AsTime())
				}
				if pay.ToTime != nil {
					d = d.Where("created_at <= ?", pay.ToTime.AsTime())
				}
				return d
			})
		return query, nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	if err := paginated.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, row := range rows {
		if row == nil {
			continue
		}
		result.Logs = append(result.Logs, toProtoBalanceChangeLog(row))
	}

	return connect.NewResponse(result), nil
}
