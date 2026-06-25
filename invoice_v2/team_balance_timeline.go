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

// TeamBalanceTimeline implements [invoice_ifaceconnect.InvoiceServiceHandler]. For
// the scoped team it returns a balance time series — one point per day/month/year
// bucket — carrying the running payable/receivable balance plus the in-bucket flow
// and accepted payments. Buckets follow Asia/Jakarta (matching the team's reports),
// and only buckets with activity are emitted, in chronological order.
//
// The running balance is reconstructed from BalanceChangeLog: TeamBalance.Balance
// is the cumulative sum of change_amount, so the balance at a bucket end is the
// opening balance (everything before the window) plus the cumulative in-window
// change. Aggregating across counterparties is just the same sum without the
// for_team_id filter. Authenticated callers only.
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
	unit, err := granularityUnit(pay.Granularity)
	if err != nil {
		return nil, err
	}
	start, end := pay.TimeRange.Start.AsTime(), pay.TimeRange.End.AsTime()
	db := s.db.WithContext(ctx)

	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Where("team_id = ?", pay.Filter.TeamId)
		if pay.Filter.ForTeamId > 0 {
			q = q.Where("for_team_id = ?", pay.Filter.ForTeamId)
		}
		return q
	}

	// 1. opening balance per type (everything before the window).
	var openRows []struct {
		BalanceType invoice_iface.BalanceType
		Val         float64
	}
	if err := scope(db.Table("balance_change_logs")).
		Where("created_at < ?", start).
		Select("balance_type, COALESCE(SUM(change_amount), 0) as val").
		Group("balance_type").
		Scan(&openRows).Error; err != nil {
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

	// bucketSet maps a bucket key (period start, micros) to its time, unioned
	// across the change and payment queries. The bucket boundary is the Jakarta
	// wall-clock instant, scanned as a UTC-labelled time (per the team convention).
	bucketSet := map[int64]time.Time{}

	// 2. in-window change per bucket per type.
	type bucketChange struct{ payable, receivable float64 }
	changes := map[int64]*bucketChange{}
	var changeRows []struct {
		T           time.Time
		BalanceType invoice_iface.BalanceType
		Val         float64
	}
	if err := scope(db.Table("balance_change_logs")).
		Where("created_at >= ? AND created_at < ?", start, end).
		Select("DATE_TRUNC('"+unit+"', created_at AT TIME ZONE 'Asia/Jakarta') as t, balance_type, SUM(change_amount) as val").
		Group("t, balance_type").
		Scan(&changeRows).Error; err != nil {
		return nil, err
	}
	for _, r := range changeRows {
		key := r.T.UnixMicro()
		bucketSet[key] = r.T
		bc := changes[key]
		if bc == nil {
			bc = &bucketChange{}
			changes[key] = bc
		}
		switch r.BalanceType {
		case btPayable:
			bc.payable = r.Val
		case btReceivable:
			bc.receivable = r.Val
		}
	}

	// 3. in-window accepted payments per bucket.
	payments := map[int64]float64{}
	var payRows []struct {
		T   time.Time
		Val float64
	}
	if err := scope(db.Table("invoice_payments")).
		Where("status = ? AND accepted_at >= ? AND accepted_at < ?", psAccepted, start, end).
		Select("DATE_TRUNC('"+unit+"', accepted_at AT TIME ZONE 'Asia/Jakarta') as t, SUM(amount) as val").
		Group("t").
		Scan(&payRows).Error; err != nil {
		return nil, err
	}
	for _, r := range payRows {
		key := r.T.UnixMicro()
		bucketSet[key] = r.T
		payments[key] = r.Val
	}

	keys := make([]int64, 0, len(bucketSet))
	for k := range bucketSet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	resp := &invoice_iface.TeamBalanceTimelineResponse{
		Points: make([]*invoice_iface.TeamBalanceTimePoint, 0, len(keys)),
	}
	for _, k := range keys {
		var dp, dr float64
		if bc := changes[k]; bc != nil {
			dp, dr = bc.payable, bc.receivable
		}
		payable += dp
		receivable += dr
		resp.Points = append(resp.Points, &invoice_iface.TeamBalanceTimePoint{
			PeriodStart:       timestamppb.New(bucketSet[k]),
			PayableBalance:    payable,
			ReceivableBalance: receivable,
			PayableChange:     dp,
			ReceivableChange:  dr,
			TotalPayment:      payments[k],
		})
	}

	return connect.NewResponse(resp), nil
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
