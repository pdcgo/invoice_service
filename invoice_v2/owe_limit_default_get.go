package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
)

// OweLimitDefaultGet implements [invoice_ifaceconnect.InvoiceServiceHandler]. It returns
// the CREDITOR team's default owe threshold — the rule applied to any debtor that has no
// custom row. configured=false means there is no default rule (the creditor allows any
// debt); threshold 0 means unlimited.
func (s *invoiceServiceImpl) OweLimitDefaultGet(
	ctx context.Context,
	req *connect.Request[invoice_iface.OweLimitDefaultGetRequest],
) (*connect.Response[invoice_iface.OweLimitDefaultGetResponse], error) {
	pay := req.Msg

	var cfg db_models.OweLimitConfiguration
	res := s.db.
		WithContext(ctx).
		Where("team_id = ? AND is_default = ?", pay.TeamId, true).
		Limit(1).
		Find(&cfg)
	if res.Error != nil {
		return nil, res.Error
	}

	return connect.NewResponse(&invoice_iface.OweLimitDefaultGetResponse{
		Configured: res.RowsAffected > 0,
		Threshold:  cfg.Threshold,
	}), nil
}
