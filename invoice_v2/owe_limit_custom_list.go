package invoice_v2

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_connect"
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

	type oweRow struct {
		ID        uint64
		ForTeamID uint64
		Threshold float64
	}
	var rows []oweRow

	const thresholdExpr = "COALESCE(olc.threshold, olcd.threshold, 0)"

	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		query := db.
			Table("teams t").
			Joins("left join owe_limit_configurations olc on olc.for_team_id = t.id and olc.team_id = ? and olc.is_default != true", pay.TeamId).
			Joins("left join owe_limit_configurations olcd on olcd.team_id = ? and olcd.is_default = true and olc.for_team_id is null", pay.TeamId).
			Where("t.id != ?", pay.TeamId).
			Where("t.type = ?", "selling")

		search := strings.TrimSpace(pay.GetFilter().GetQ())
		if search != "" {
			query = query.Where("t.name ILIKE ?", "%"+search+"%")
		}

		return query.Select([]string{
			"COALESCE(olc.id, olcd.id) as id",
			"t.id as for_team_id",
			thresholdExpr + " as threshold",
		}), nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	// Sort column, defaulting to threshold when unspecified. Team name breaks ties so
	// the order — and therefore paging — is stable.
	var orderCol string
	switch pay.GetSort().GetType() {
	case invoice_iface.OweLimitCustomSortType_OWE_LIMIT_CUSTOM_SORT_TYPE_THRESHOLD:
		orderCol = thresholdExpr
	default:
		orderCol = thresholdExpr
	}

	orderDir := "desc"
	if pay.GetSort().GetSortType() == invoice_iface.SortType_SORT_TYPE_ASC {
		orderDir = "asc"
	}

	err = paginated.
		Order(orderCol + " " + orderDir + " nulls last, t.name asc").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, row := range rows {
		result.Items = append(result.Items, &invoice_iface.OweLimitCustomItem{
			Id:        row.ID,
			ForTeamId: row.ForTeamID,
			Threshold: row.Threshold,
		})
	}

	return connect.NewResponse(result), nil
}
