package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
)

// OweLimitCustomDelete implements [invoice_ifaceconnect.InvoiceServiceHandler]. It removes
// the CREDITOR team's per-debtor threshold for for_team_id; that debtor then falls back to
// the creditor's default rule (or, with no default, is allowed). Hard delete — the model
// has no soft-delete field. Deleting a row that does not exist is a no-op.
func (s *invoiceServiceImpl) OweLimitCustomDelete(
	ctx context.Context,
	req *connect.Request[invoice_iface.OweLimitCustomDeleteRequest],
) (*connect.Response[invoice_iface.OweLimitCustomDeleteResponse], error) {
	pay := req.Msg

	err := s.db.
		WithContext(ctx).
		Where("team_id = ? AND for_team_id = ? AND is_default IS NOT TRUE", pay.TeamId, pay.ForTeamId).
		Delete(&db_models.OweLimitConfiguration{}).
		Error
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.OweLimitCustomDeleteResponse{}), nil
}
