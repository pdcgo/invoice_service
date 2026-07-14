package invoice_v2

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	common "github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

const (
	btPayable    = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
	btReceivable = invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
	psAccepted   = invoice_iface.PaymentStatus_PAYMENT_STATUS_ACCEPTED
)

// TeamBalanceList implements [invoice_ifaceconnect.InvoiceServiceHandler]. For the
// scoped team (filter.team_id) it returns its counterparty teams (for_team_id)
// sorted by the requested column and paginated — the sort defines membership — plus
// one keyed data map per requested data_type. Authenticated callers only.
func (s *invoiceServiceImpl) TeamBalanceList(
	ctx context.Context,
	req *connect.Request[invoice_iface.TeamBalanceListRequest],
) (*connect.Response[invoice_iface.TeamBalanceListResponse], error) {
	pay := req.Msg
	if pay.Filter == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter is required"))
	}
	if pay.Sort == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sort is required"))
	}
	if pay.TimeRange == nil || pay.TimeRange.Start == nil || pay.TimeRange.End == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("time_range is required"))
	}
	start, end := pay.TimeRange.Start.AsTime(), pay.TimeRange.End.AsTime()
	db := s.db.WithContext(ctx)

	ids, err := sortForTeamIDs(db, pay.Filter, pay.Sort, start, end)
	if err != nil {
		return nil, err
	}

	resp := &invoice_iface.TeamBalanceListResponse{
		Data: make([]*invoice_iface.TeamBalanceData, 0, len(pay.DataTypes)),
		Ids:  ids,
	}
	for _, dt := range pay.DataTypes {
		data, err := fetchTeamBalanceData(db, pay.Filter.TeamId, dt, ids, start, end)
		if err != nil {
			return nil, err
		}
		resp.Data = append(resp.Data, data)
	}

	return connect.NewResponse(resp), nil
}

// sortForTeamIDs resolves the ordered, paginated page of counterparty for_team_ids
// according to the sort. Each sort variant queries its own source scoped to the
// team (and optionally for_team_id / team_type), so the sort defines membership.
func sortForTeamIDs(
	db *gorm.DB,
	filter *invoice_iface.TeamBalanceListFilter,
	sort *invoice_iface.TeamBalanceListSort,
	start, end time.Time,
) ([]uint64, error) {
	dir := "asc nulls last"
	if sort.SortType == invoice_iface.SortType_SORT_TYPE_DESC {
		dir = "desc nulls last"
	}
	limit, offset := pageLimitOffset(filter.GetPage())
	typeFilter := dbTeamType(filter.GetTeamType())
	search := strings.TrimSpace(filter.GetQ())

	// Both the type filter and the name search read columns off teams, so either
	// one requires the join. Without this the search would silently match
	// everything on the sorts that don't otherwise join teams.
	needTeamJoin := typeFilter != "" || search != ""

	// scope adds team_id / for_team_id / optional teams.type + name filters onto a
	// query whose primary table is aliased "x" with a for_team_id column.
	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Where("x.team_id = ?", filter.TeamId)
		if filter.ForTeamId > 0 {
			q = q.Where("x.for_team_id = ?", filter.ForTeamId)
		}
		if needTeamJoin {
			q = q.Joins("JOIN teams t ON t.id = x.for_team_id")
		}
		if typeFilter != "" {
			q = q.Where("t.type = ?", typeFilter)
		}
		if search != "" {
			q = q.Where("t.name ILIKE ?", "%"+search+"%")
		}
		return q
	}

	var ids []uint64
	var err error

	switch sv := sort.S.(type) {
	case *invoice_iface.TeamBalanceListSort_Common:
		col := "t.name"
		if sv.Common == invoice_iface.TeamBalanceCommonSort_TEAM_BALANCE_COMMON_SORT_TEAM_TYPE {
			col = "t.type"
		}
		// This branch already joins teams to sort on it, so it takes the type and
		// name predicates directly — routing it through scope would join twice.
		q := db.Table("team_balances x").
			Joins("JOIN teams t ON t.id = x.for_team_id").
			Where("x.team_id = ?", filter.TeamId)
		if filter.ForTeamId > 0 {
			q = q.Where("x.for_team_id = ?", filter.ForTeamId)
		}
		if typeFilter != "" {
			q = q.Where("t.type = ?", typeFilter)
		}
		if search != "" {
			q = q.Where("t.name ILIKE ?", "%"+search+"%")
		}
		err = q.
			Group("x.for_team_id, "+col).
			Order(col + " " + dir).
			Limit(limit).
			Offset(offset).
			Pluck("x.for_team_id", &ids).
			Error

	case *invoice_iface.TeamBalanceListSort_Payable:
		err = scope(db.Table("team_balances x").Where("x.balance_type = ?", btPayable)).
			Order("x.balance "+dir).Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error
	case *invoice_iface.TeamBalanceListSort_Receivable:
		err = scope(db.Table("team_balances x").Where("x.balance_type = ?", btReceivable)).
			Order("x.balance "+dir).Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error
	case *invoice_iface.TeamBalanceListSort_PendingPayment:
		err = scope(db.Table("team_balances x").Where("x.balance_type = ?", btPayable)).
			Order("x.pending_payment_amount "+dir).Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error
	case *invoice_iface.TeamBalanceListSort_IncomingPayment:
		err = scope(db.Table("team_balances x").Where("x.balance_type = ?", btReceivable)).
			Order("x.pending_payment_amount "+dir).Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error

	case *invoice_iface.TeamBalanceListSort_TotalPayment:
		err = scope(db.Table("invoice_payments x").
			Where("x.status = ? AND x.accepted_at BETWEEN ? AND ?", psAccepted, start, end)).
			Group("x.for_team_id").Order("SUM(x.amount) "+dir).
			Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error
	case *invoice_iface.TeamBalanceListSort_TotalPayable:
		err = scope(db.Table("balance_change_logs x").
			Where("x.balance_type = ? AND x.created_at BETWEEN ? AND ?", btPayable, start, end)).
			Group("x.for_team_id").Order("SUM(x.change_amount) "+dir).
			Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error
	case *invoice_iface.TeamBalanceListSort_TotalReceivable:
		err = scope(db.Table("balance_change_logs x").
			Where("x.balance_type = ? AND x.created_at BETWEEN ? AND ?", btReceivable, start, end)).
			Group("x.for_team_id").Order("SUM(x.change_amount) "+dir).
			Limit(limit).Offset(offset).Pluck("x.for_team_id", &ids).Error

	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sort is required"))
	}

	return ids, err
}

// fetchTeamBalanceData builds the data map for one data_type, keyed by for_team_id,
// over the given (already sorted/paginated) ids.
func fetchTeamBalanceData(
	db *gorm.DB,
	teamID uint64,
	dt invoice_iface.TeamBalanceListDataType,
	ids []uint64,
	start, end time.Time,
) (*invoice_iface.TeamBalanceData, error) {
	switch dt {
	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON:
		out := map[uint64]*invoice_iface.TeamBalanceCommonItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID   uint64
				Name string
				Type db_models.TeamType
			}
			if err := db.Table("teams").Select("id, name, type").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.ID] = &invoice_iface.TeamBalanceCommonItem{Id: r.ID, Name: r.Name, Type: teamTypeToProto(r.Type)}
			}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_Common{
			Common: &invoice_iface.TeamBalanceCommonData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_PAYABLE:
		m, err := scalarMap(balanceQuery(db, teamID, ids, btPayable, "balance"))
		if err != nil {
			return nil, err
		}
		out := map[uint64]*invoice_iface.TeamBalancePayableItem{}
		for id, v := range m {
			out[id] = &invoice_iface.TeamBalancePayableItem{Balance: v}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_Payable{
			Payable: &invoice_iface.TeamBalancePayableData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_RECEIVABLE:
		m, err := scalarMap(balanceQuery(db, teamID, ids, btReceivable, "balance"))
		if err != nil {
			return nil, err
		}
		out := map[uint64]*invoice_iface.TeamBalanceReceivableItem{}
		for id, v := range m {
			out[id] = &invoice_iface.TeamBalanceReceivableItem{Balance: v}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_Receivable{
			Receivable: &invoice_iface.TeamBalanceReceivableData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_PENDING_PAYMENT:
		m, err := scalarMap(balanceQuery(db, teamID, ids, btPayable, "pending_payment_amount"))
		if err != nil {
			return nil, err
		}
		out := map[uint64]*invoice_iface.TeamBalancePendingPaymentItem{}
		for id, v := range m {
			out[id] = &invoice_iface.TeamBalancePendingPaymentItem{Amount: v}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_PendingPayment{
			PendingPayment: &invoice_iface.TeamBalancePendingPaymentData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_INCOMING_PAYMENT:
		m, err := scalarMap(balanceQuery(db, teamID, ids, btReceivable, "pending_payment_amount"))
		if err != nil {
			return nil, err
		}
		out := map[uint64]*invoice_iface.TeamBalanceIncomingPaymentItem{}
		for id, v := range m {
			out[id] = &invoice_iface.TeamBalanceIncomingPaymentItem{Amount: v}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_IncomingPayment{
			IncomingPayment: &invoice_iface.TeamBalanceIncomingPaymentData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_TOTAL_PAYMENT:
		m, err := scalarMap(scoped(db.Table("invoice_payments"), teamID, ids).
			Where("status = ? AND accepted_at BETWEEN ? AND ?", psAccepted, start, end).
			Select("for_team_id, SUM(amount) as val").Group("for_team_id"))
		if err != nil {
			return nil, err
		}
		out := map[uint64]*invoice_iface.TeamBalanceTotalPaymentItem{}
		for id, v := range m {
			out[id] = &invoice_iface.TeamBalanceTotalPaymentItem{Amount: v}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_TotalPayment{
			TotalPayment: &invoice_iface.TeamBalanceTotalPaymentData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_TOTAL_PAYABLE:
		out, err := totalChangeData(db, teamID, ids, btPayable, start, end)
		if err != nil {
			return nil, err
		}
		payable := map[uint64]*invoice_iface.TeamBalanceTotalPayableItem{}
		for id, it := range out {
			payable[id] = &invoice_iface.TeamBalanceTotalPayableItem{TotalAmount: it.total, Change: it.change}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_TotalPayable{
			TotalPayable: &invoice_iface.TeamBalanceTotalPayableData{Data: payable},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_TOTAL_RECEIVABLE:
		out, err := totalChangeData(db, teamID, ids, btReceivable, start, end)
		if err != nil {
			return nil, err
		}
		recv := map[uint64]*invoice_iface.TeamBalanceTotalReceivableItem{}
		for id, it := range out {
			recv[id] = &invoice_iface.TeamBalanceTotalReceivableItem{TotalAmount: it.total, Change: it.change}
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_TotalReceivable{
			TotalReceivable: &invoice_iface.TeamBalanceTotalReceivableData{Data: recv},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_OWE_LIMIT_AS_CREDITOR:
		// The scoped team is the creditor: what it lets each counterparty owe it.
		evals, err := EvaluateOweLimitsAsCreditor(db, teamID, ids)
		if err != nil {
			return nil, err
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_OweLimitAsCreditor{
			OweLimitAsCreditor: &invoice_iface.TeamBalanceOweLimitAsCreditorData{Data: oweLimitItems(evals)},
		}}, nil

	case invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_OWE_LIMIT_AS_DEBTOR:
		// The scoped team is the debtor: what each counterparty lets it owe them.
		evals, err := EvaluateOweLimitsAsDebtor(db, teamID, ids)
		if err != nil {
			return nil, err
		}
		return &invoice_iface.TeamBalanceData{Data: &invoice_iface.TeamBalanceData_OweLimitAsDebtor{
			OweLimitAsDebtor: &invoice_iface.TeamBalanceOweLimitAsDebtorData{Data: oweLimitItems(evals)},
		}}, nil

	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid data type"))
	}
}

// oweLimitItems converts resolved owe limits to their proto items, keyed by
// counterparty id.
func oweLimitItems(evals map[uint64]*OweLimitEval) map[uint64]*invoice_iface.TeamBalanceOweLimitItem {
	out := make(map[uint64]*invoice_iface.TeamBalanceOweLimitItem, len(evals))
	for id, e := range evals {
		out[id] = &invoice_iface.TeamBalanceOweLimitItem{
			Threshold:    e.Threshold,
			ActiveAmount: e.ActiveAmount,
			Allow:        e.Allow,
			Configured:   e.Configured,
			IsDefault:    e.IsDefault,
		}
	}
	return out
}

// scoped restricts a query to the team and the given for_team_ids.
func scoped(q *gorm.DB, teamID uint64, ids []uint64) *gorm.DB {
	return q.Where("team_id = ? AND for_team_id IN ?", teamID, ids)
}

// balanceQuery selects (for_team_id, <col> as val) from team_balances for a type.
func balanceQuery(db *gorm.DB, teamID uint64, ids []uint64, bt invoice_iface.BalanceType, col string) *gorm.DB {
	return scoped(db.Table("team_balances"), teamID, ids).
		Where("balance_type = ?", bt).
		Select("for_team_id, " + col + " as val")
}

// scalarMap scans rows of (for_team_id, val) into a map.
func scalarMap(q *gorm.DB) (map[uint64]float64, error) {
	m := map[uint64]float64{}
	var rows []struct {
		ForTeamID uint64
		Val       float64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		m[r.ForTeamID] = r.Val
	}
	return m, nil
}

type totalChangeItem struct {
	total  float64
	change []*invoice_iface.ChangeSumAmount
}

// totalChangeData aggregates balance_change_logs in the window per for_team_id:
// the total change_amount and the per-change_type breakdown.
func totalChangeData(
	db *gorm.DB,
	teamID uint64,
	ids []uint64,
	bt invoice_iface.BalanceType,
	start, end time.Time,
) (map[uint64]*totalChangeItem, error) {
	out := map[uint64]*totalChangeItem{}
	if len(ids) == 0 {
		return out, nil
	}

	base := func() *gorm.DB {
		return scoped(db.Table("balance_change_logs"), teamID, ids).
			Where("balance_type = ? AND created_at BETWEEN ? AND ?", bt, start, end)
	}

	totals, err := scalarMap(base().Select("for_team_id, SUM(change_amount) as val").Group("for_team_id"))
	if err != nil {
		return nil, err
	}
	for id, v := range totals {
		out[id] = &totalChangeItem{total: v}
	}

	var rows []struct {
		ForTeamID        uint64
		ChangeType       invoice_iface.BalanceChangeType
		Val              float64
		TransactionCount int64
	}
	err = base().
		Select("for_team_id, change_type, SUM(change_amount) as val, COUNT(*) as transaction_count").
		Group("for_team_id, change_type").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		it := out[r.ForTeamID]
		if it == nil {
			it = &totalChangeItem{}
			out[r.ForTeamID] = it
		}
		it.change = append(it.change, &invoice_iface.ChangeSumAmount{
			ChangeType:       r.ChangeType,
			Amount:           r.Val,
			TransactionCount: r.TransactionCount,
		})
	}

	return out, nil
}

// pageLimitOffset converts a PageFilter to gorm Limit/Offset (default 100/0).
func pageLimitOffset(p *common.PageFilter) (int, int) {
	if p == nil || p.Limit <= 0 {
		return 100, 0
	}
	offset := (p.Page - 1) * p.Limit
	if offset < 0 {
		offset = 0
	}
	return int(p.Limit), int(offset)
}

// dbTeamType maps the proto TeamType filter to the stored teams.type string,
// or "" when unspecified (no filter).
func dbTeamType(t common.TeamType) string {
	switch t {
	case common.TeamType_TEAM_TYPE_WAREHOUSE:
		return string(db_models.WarehouseTeamType)
	case common.TeamType_TEAM_TYPE_SELLING:
		return string(db_models.SellingTeamType)
	case common.TeamType_TEAM_TYPE_ADMIN:
		return string(db_models.AdminTeamType)
	default:
		return ""
	}
}

// teamTypeToProto maps the stored team type to the proto enum (root reported as
// admin, matching the proto which has no root variant).
func teamTypeToProto(t db_models.TeamType) common.TeamType {
	switch t {
	case db_models.WarehouseTeamType:
		return common.TeamType_TEAM_TYPE_WAREHOUSE
	case db_models.SellingTeamType:
		return common.TeamType_TEAM_TYPE_SELLING
	case db_models.AdminTeamType, db_models.RootTeamType:
		return common.TeamType_TEAM_TYPE_ADMIN
	default:
		return common.TeamType_TEAM_TYPE_UNSPECIFIED
	}
}
