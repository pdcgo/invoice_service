package invoice_v2

import (
	"context"
	"errors"
	"sort"
	"time"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// timelineBucket is one period bucket carrying its end-of-period running balances plus the
// in-period net flow (with a per-change_type breakdown) and accepted payments. In the
// response it is keyed by id = period_start unix seconds (the guideline's uint64 map key),
// with the exact period_start timestamp surfaced through the COMMON data type.
type timelineBucket struct {
	id                    uint64
	periodStart           time.Time
	payableBalance        float64
	receivableBalance     float64
	payableChange         float64
	receivableChange      float64
	payableChangeBreak    []*invoice_iface.ChangeSumAmount
	receivableChangeBreak []*invoice_iface.ChangeSumAmount
	totalPayment          float64
}

// TeamBalanceTimeline implements [invoice_ifaceconnect.InvoiceServiceHandler]. For the
// scoped team it returns a balance time series — one bucket per day/month/year — carrying
// the requested metrics (data_types) as keyed maps plus the sorted bucket ids, following
// the list proto guideline (see docs/proto-guideline.md, mirroring TeamBalanceList).
//
// Buckets follow Asia/Jakarta (fixed UTC+7); each period key/period_start is the absolute
// instant of that Jakarta period start, computed in SQL so it does not depend on the
// process/DB timezone. The window's start is floored to its Jakarta day, so this is a
// day/month/year statistic. The running balance is reconstructed as an opening balance
// (everything before the window, summed from the maintained TeamBalanceDailyLog rollup)
// plus the cumulative in-window net change per bucket (from BalanceChangeLog, which also
// yields the per-change_type breakdown). Only buckets with activity are emitted; aggregating
// across counterparties is the same sum without the for_team_id filter. Authenticated only.
func (s *invoiceServiceImpl) TeamBalanceTimeline(
	ctx context.Context,
	req *connect.Request[invoice_iface.TeamBalanceTimelineRequest],
) (*connect.Response[invoice_iface.TeamBalanceTimelineResponse], error) {
	pay := req.Msg
	if pay.Filter == nil || pay.Filter.TeamId == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.team_id is required"))
	}
	if pay.TimeRange == nil || pay.TimeRange.Start == nil || pay.TimeRange.End == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("time_range is required"))
	}
	if len(pay.DataTypes) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("data_types is required"))
	}
	unit, err := granularityUnit(pay.Granularity)
	if err != nil {
		return nil, err
	}
	// A day/month/year statistic: floor the start to the Jakarta day so the opening-balance
	// cutoff and the in-window lower bound are the same instant (no seam between them).
	startDay := startOfJakartaDay(pay.TimeRange.Start.AsTime())
	end := pay.TimeRange.End.AsTime()
	db := s.db.WithContext(ctx)

	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Where("team_id = ?", pay.Filter.TeamId)
		if pay.Filter.ForTeamId > 0 {
			q = q.Where("for_team_id = ?", pay.Filter.ForTeamId)
		}
		return q
	}

	// bucketExpr truncates a timestamptz column to the Jakarta period start and converts it
	// back to a timestamptz, so the scanned instant (hence the bucket id / period_start) is
	// stable regardless of the driver/session timezone. unit is validated (day/month/year).
	bucketExpr := func(col string) string {
		return "DATE_TRUNC('" + unit + "', " + col + " AT TIME ZONE 'Asia/Jakarta') AT TIME ZONE 'Asia/Jakarta'"
	}

	// 1. opening balance per type (everything before the window) from the daily rollup.
	// SUM(change_amount) equals replaying balance_change_logs (both written in one tx) but
	// over far fewer rows, and is aggregation-safe across counterparties.
	var openRows []struct {
		BalanceType invoice_iface.BalanceType
		Val         float64
	}
	err = scope(db.Table("team_balance_daily_logs")).
		Where("day < ?", startDay).
		Select("balance_type, COALESCE(SUM(change_amount), 0) as val").
		Group("balance_type").
		Scan(&openRows).
		Error
	if err != nil {
		return nil, err
	}
	payable, receivable := 0.0, 0.0
	for _, r := range openRows {
		switch r.BalanceType {
		case btPayable:
			payable = r.Val
		case btReceivable:
			receivable = r.Val
		}
	}

	// bucketSet unions the bucket keys (period_start unix seconds) across the change and
	// payment queries; the value is the bucket's period-start instant.
	bucketSet := map[int64]time.Time{}

	// 2. in-window change per bucket per (balance_type, change_type): the per-change_type
	// breakdown, whose sum over change types is the bucket's net change for that type.
	type bucketChange struct {
		payable, receivable       float64
		payableBrk, receivableBrk []*invoice_iface.ChangeSumAmount
	}
	changes := map[int64]*bucketChange{}
	var changeRows []struct {
		T           time.Time
		BalanceType invoice_iface.BalanceType
		ChangeType  invoice_iface.BalanceChangeType
		Val         float64
		Cnt         int64
	}
	err = scope(db.Table("balance_change_logs")).
		Where("created_at >= ? AND created_at < ?", startDay, end).
		Select(bucketExpr("created_at") + " as t, balance_type, change_type, SUM(change_amount) as val, COUNT(*) as cnt").
		Group("t, balance_type, change_type").
		Scan(&changeRows).
		Error
	if err != nil {
		return nil, err
	}
	for _, r := range changeRows {
		key := r.T.Unix()
		bucketSet[key] = r.T
		bc := changes[key]
		if bc == nil {
			bc = &bucketChange{}
			changes[key] = bc
		}
		entry := &invoice_iface.ChangeSumAmount{
			ChangeType:       r.ChangeType,
			Amount:           r.Val,
			TransactionCount: r.Cnt,
		}
		switch r.BalanceType {
		case btPayable:
			bc.payable += r.Val
			bc.payableBrk = append(bc.payableBrk, entry)
		case btReceivable:
			bc.receivable += r.Val
			bc.receivableBrk = append(bc.receivableBrk, entry)
		}
	}

	// 3. in-window accepted payments per bucket.
	payments := map[int64]float64{}
	var payRows []struct {
		T   time.Time
		Val float64
	}
	err = scope(db.Table("invoice_payments")).
		Where("status = ? AND accepted_at >= ? AND accepted_at < ?", psAccepted, startDay, end).
		Select(bucketExpr("accepted_at") + " as t, SUM(amount) as val").
		Group("t").
		Scan(&payRows).
		Error
	if err != nil {
		return nil, err
	}
	for _, r := range payRows {
		key := r.T.Unix()
		bucketSet[key] = r.T
		payments[key] = r.Val
	}

	// Chronological keys — the running balance must accumulate in time order regardless of
	// the requested output sort.
	keys := make([]int64, 0, len(bucketSet))
	for k := range bucketSet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	buckets := make([]timelineBucket, 0, len(keys))
	for _, k := range keys {
		var dp, dr float64
		var pbrk, rbrk []*invoice_iface.ChangeSumAmount
		if bc := changes[k]; bc != nil {
			dp, dr = bc.payable, bc.receivable
			pbrk, rbrk = bc.payableBrk, bc.receivableBrk
		}
		payable += dp
		receivable += dr
		buckets = append(buckets, timelineBucket{
			id:                    uint64(k),
			periodStart:           bucketSet[k],
			payableBalance:        payable,
			receivableBalance:     receivable,
			payableChange:         dp,
			receivableChange:      dr,
			payableChangeBreak:    pbrk,
			receivableChangeBreak: rbrk,
			totalPayment:          payments[k],
		})
	}

	// ids in the requested sort direction (default period ascending).
	ids := make([]uint64, len(buckets))
	if timelineSortDesc(pay.Sort) {
		for i, b := range buckets {
			ids[len(buckets)-1-i] = b.id
		}
	} else {
		for i, b := range buckets {
			ids[i] = b.id
		}
	}

	resp := &invoice_iface.TeamBalanceTimelineResponse{
		Data: make([]*invoice_iface.TeamBalanceTimelineData, 0, len(pay.DataTypes)),
		Ids:  ids,
	}
	for _, dt := range pay.DataTypes {
		data, derr := timelineDataFor(dt, buckets)
		if derr != nil {
			return nil, derr
		}
		resp.Data = append(resp.Data, data)
	}

	return connect.NewResponse(resp), nil
}

// timelineSortDesc reports whether the bucket ids should be emitted period-descending.
func timelineSortDesc(sortReq *invoice_iface.TeamBalanceTimelineSort) bool {
	return sortReq != nil && sortReq.SortType == invoice_iface.SortType_SORT_TYPE_DESC
}

// timelineDataFor builds the keyed map for one data_type over the ordered buckets, keyed by
// bucket id (mirrors fetchTeamBalanceData in team_balance_list.go).
func timelineDataFor(
	dt invoice_iface.TeamBalanceTimelineDataType,
	buckets []timelineBucket,
) (*invoice_iface.TeamBalanceTimelineData, error) {
	switch dt {
	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_COMMON:
		out := map[uint64]*invoice_iface.TeamBalanceTimelineCommonItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelineCommonItem{PeriodStart: timestamppb.New(b.periodStart)}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_Common{
			Common: &invoice_iface.TeamBalanceTimelineCommonData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_PAYABLE_BALANCE:
		out := map[uint64]*invoice_iface.TeamBalanceTimelinePayableBalanceItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelinePayableBalanceItem{Balance: b.payableBalance}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_PayableBalance{
			PayableBalance: &invoice_iface.TeamBalanceTimelinePayableBalanceData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_RECEIVABLE_BALANCE:
		out := map[uint64]*invoice_iface.TeamBalanceTimelineReceivableBalanceItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelineReceivableBalanceItem{Balance: b.receivableBalance}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_ReceivableBalance{
			ReceivableBalance: &invoice_iface.TeamBalanceTimelineReceivableBalanceData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_PAYABLE_CHANGE:
		out := map[uint64]*invoice_iface.TeamBalanceTimelinePayableChangeItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelinePayableChangeItem{Amount: b.payableChange, Change: b.payableChangeBreak}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_PayableChange{
			PayableChange: &invoice_iface.TeamBalanceTimelinePayableChangeData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_RECEIVABLE_CHANGE:
		out := map[uint64]*invoice_iface.TeamBalanceTimelineReceivableChangeItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelineReceivableChangeItem{Amount: b.receivableChange, Change: b.receivableChangeBreak}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_ReceivableChange{
			ReceivableChange: &invoice_iface.TeamBalanceTimelineReceivableChangeData{Data: out},
		}}, nil

	case invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_TOTAL_PAYMENT:
		out := map[uint64]*invoice_iface.TeamBalanceTimelineTotalPaymentItem{}
		for _, b := range buckets {
			out[b.id] = &invoice_iface.TeamBalanceTimelineTotalPaymentItem{Amount: b.totalPayment}
		}
		return &invoice_iface.TeamBalanceTimelineData{Data: &invoice_iface.TeamBalanceTimelineData_TotalPayment{
			TotalPayment: &invoice_iface.TeamBalanceTimelineTotalPaymentData{Data: out},
		}}, nil

	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid data type"))
	}
}

// granularityUnit maps the proto granularity to a Postgres date_trunc unit.
func granularityUnit(g invoice_iface.TimeGranularity) (string, error) {
	switch g {
	case invoice_iface.TimeGranularity_TIME_GRANULARITY_DAILY:
		return "day", nil
	case invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY:
		return "month", nil
	case invoice_iface.TimeGranularity_TIME_GRANULARITY_YEARLY:
		return "year", nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("granularity is required"))
	}
}
