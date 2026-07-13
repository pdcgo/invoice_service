package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_connect"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// OweLimitCustomList implements [invoice_ifaceconnect.InvoiceServiceHandler]. It lists the
// CREDITOR team's per-debtor owe thresholds (the custom rows only — the default rule is
// fetched with OweLimitDefaultGet), newest first, paginated.
func (s *invoiceServiceImpl) OweLimitCustomList(
	ctx context.Context,
	req *connect.Request[invoice_iface.OweLimitCustomListRequest],
) (*connect.Response[invoice_iface.OweLimitCustomListResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &invoice_iface.OweLimitCustomListResponse{
		Items:    []*invoice_iface.OweLimitCustomItem{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []db_models.OweLimitConfiguration
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		query := db.
			Model(&db_models.OweLimitConfiguration{}).
			Scopes(func(d *gorm.DB) *gorm.DB {
				// is_default IS NOT TRUE: the column is nullable in the live schema and
				// gorm scans NULL to false, so a NULL row counts as custom.
				return d.
					Where("team_id = ?", pay.TeamId).
					Where("is_default IS NOT TRUE").
					Where("for_team_id IS NOT NULL")
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
		item := &invoice_iface.OweLimitCustomItem{
			Id:        row.ID,
			Threshold: row.Threshold,
		}
		if row.ForTeamID != nil {
			item.ForTeamId = *row.ForTeamID
		}
		result.Items = append(result.Items, item)
	}

	return connect.NewResponse(result), nil
}
