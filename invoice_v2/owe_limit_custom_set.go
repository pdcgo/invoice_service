package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// OweLimitCustomSet implements [invoice_ifaceconnect.InvoiceServiceHandler]. It upserts a
// per-debtor owe threshold for the CREDITOR team (team_id): "for_team_id may owe me up to
// threshold" (0 = unlimited). A custom row beats the creditor's default row.
func (s *invoiceServiceImpl) OweLimitCustomSet(
	ctx context.Context,
	req *connect.Request[invoice_iface.OweLimitCustomSetRequest],
) (*connect.Response[invoice_iface.OweLimitCustomSetResponse], error) {
	pay := req.Msg

	if pay.TeamId == pay.ForTeamId {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("team_id and for_team_id must differ"))
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// is_default IS NOT TRUE (not `= false`): the column is nullable in the live
		// schema, and gorm scans NULL to false — i.e. a NULL row is a custom row.
		var cfg db_models.OweLimitConfiguration
		res := lockForUpdate(tx).
			Where("team_id = ? AND for_team_id = ? AND is_default IS NOT TRUE", pay.TeamId, pay.ForTeamId).
			Limit(1).
			Find(&cfg)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			forTeamID := pay.ForTeamId
			cfg = db_models.OweLimitConfiguration{
				TeamID:    pay.TeamId,
				ForTeamID: &forTeamID,
				IsDefault: false,
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

	return connect.NewResponse(&invoice_iface.OweLimitCustomSetResponse{}), nil
}
