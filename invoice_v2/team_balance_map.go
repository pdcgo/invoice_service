package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"gorm.io/gorm"
)

// TeamBalanceMap implements [invoice_ifaceconnect.InvoiceServiceHandler]. It is
// the batch/map sibling of ListTeamBalance: it returns the scoped team's running
// balances keyed by counterparty (for_team_id) for quick balances[forTeamId]
// lookups, optionally narrowed to a set of counterparties and/or a balance_type.
//
// The map key is for_team_id, so when balance_type is unspecified and a
// counterparty has both PAYABLE and RECEIVABLE rows only one survives (the
// higher-id row wins, due to the ascending order). Callers wanting a single
// deterministic entry per counterparty should pass a balance_type.
func (s *invoiceServiceImpl) TeamBalanceMap(
	ctx context.Context,
	req *connect.Request[invoice_iface.TeamBalanceMapRequest],
) (*connect.Response[invoice_iface.TeamBalanceMapResponse], error) {
	pay := req.Msg

	result := &invoice_iface.TeamBalanceMapResponse{
		Balances: map[uint64]*invoice_iface.TeamBalance{},
	}
	db := s.db.WithContext(ctx)

	var rows []*invoice_models.TeamBalance
	query := db.
		Model(&invoice_models.TeamBalance{}).
		Scopes(func(d *gorm.DB) *gorm.DB {
			d = d.Where("team_id = ?", pay.TeamId)
			if len(pay.ForTeamIds) > 0 {
				d = d.Where("for_team_id IN ?", pay.ForTeamIds)
			}
			if pay.BalanceType != invoice_iface.BalanceType_BALANCE_TYPE_UNSPECIFIED {
				d = d.Where("balance_type = ?", pay.BalanceType)
			}
			return d
		})

	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		if row == nil {
			continue
		}
		result.Balances[row.ForTeamID] = toProtoTeamBalance(row)
	}

	return connect.NewResponse(result), nil
}
