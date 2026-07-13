package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// OweLimitDefaultSet implements [invoice_ifaceconnect.InvoiceServiceHandler]. It upserts
// the CREDITOR team's default owe threshold (0 = unlimited) — the rule applied to any
// debtor with no custom row. The row is locked for update so concurrent sets don't
// duplicate it (the partial unique index is the DB-level guard).
func (s *invoiceServiceImpl) OweLimitDefaultSet(
	ctx context.Context,
	req *connect.Request[invoice_iface.OweLimitDefaultSetRequest],
) (*connect.Response[invoice_iface.OweLimitDefaultSetResponse], error) {
	pay := req.Msg

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cfg db_models.OweLimitConfiguration
		res := lockForUpdate(tx).
			Where("team_id = ? AND is_default = ?", pay.TeamId, true).
			Limit(1).
			Find(&cfg)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			cfg = db_models.OweLimitConfiguration{
				TeamID:    pay.TeamId,
				IsDefault: true,
				Threshold: pay.Threshold,
			}
			return tx.Create(&cfg).Error
		}

		return tx.
			Model(&db_models.OweLimitConfiguration{}).
			Where("id = ?", cfg.ID).
			Update("threshold", pay.Threshold).
			Error
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.OweLimitDefaultSetResponse{}), nil
}
