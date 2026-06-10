package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"gorm.io/gorm"
)

// ListTeamBalance implements [invoice_ifaceconnect.InvoiceServiceHandler]. It
// lists the running balances of the scoped team (team_id), optionally filtered by
// counterparty (for_team_id) and balance_type. The result is unpaginated — a team
// holds at most a handful of balance rows.
func (s *invoiceServiceImpl) ListTeamBalance(
	ctx context.Context,
	req *connect.Request[invoice_iface.ListTeamBalanceRequest],
) (*connect.Response[invoice_iface.ListTeamBalanceResponse], error) {
	pay := req.Msg

	result := &invoice_iface.ListTeamBalanceResponse{
		Balances: []*invoice_iface.TeamBalance{},
	}
	db := s.db.WithContext(ctx)

	var rows []*invoice_models.TeamBalance
	query := db.
		Model(&invoice_models.TeamBalance{}).
		Scopes(func(d *gorm.DB) *gorm.DB {
			d = d.Where("team_id = ?", pay.TeamId)
			if pay.ForTeamId > 0 {
				d = d.Where("for_team_id = ?", pay.ForTeamId)
			}
			if pay.BalanceType != invoice_iface.BalanceType_BALANCE_TYPE_UNSPECIFIED {
				d = d.Where("balance_type = ?", pay.BalanceType)
			}
			return d
		})

	if err := query.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		if row == nil {
			continue
		}
		result.Balances = append(result.Balances, toProtoTeamBalance(row))
	}

	return connect.NewResponse(result), nil
}
