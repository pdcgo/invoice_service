package invoice_v2

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"gorm.io/gorm"
)

// Overview implements [invoice_ifaceconnect.InvoiceServiceHandler]. It returns one
// OverviewDataItem per requested metric, in the requested order. The current-state
// metrics (payable, receivable, pending payment) read the live TeamBalance totals;
// the windowed totals (total payable/receivable/payment) aggregate flow within
// time_range. Payable-side amounts are returned as positive magnitudes. Results are
// optionally narrowed by filter.team_id / filter.for_team_id.
func (s *invoiceServiceImpl) Overview(
	ctx context.Context,
	req *connect.Request[invoice_iface.OverviewRequest],
) (*connect.Response[invoice_iface.OverviewResponse], error) {
	pay := req.Msg
	filter := pay.GetFilter()
	db := s.db.WithContext(ctx)

	// scope narrows a query by the requested team / counterparty.
	scope := func(q *gorm.DB) *gorm.DB {
		if filter != nil {
			if filter.TeamId > 0 {
				q = q.Where("team_id = ?", filter.TeamId)
			}
			if filter.ForTeamId > 0 {
				q = q.Where("for_team_id = ?", filter.ForTeamId)
			}
		}
		return q
	}

	sumCol := func(q *gorm.DB, expr string) (float64, error) {
		var v float64
		err := q.Select("COALESCE(SUM(" + expr + "), 0)").Scan(&v).Error
		return v, err
	}

	// timeWindow resolves the required [start, end] for the windowed metrics.
	var start, end time.Time
	timeWindow := func() error {
		tr := pay.GetTimeRange()
		if tr == nil || tr.GetStart() == nil || tr.GetEnd() == nil {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("time_range is required"))
		}
		start, end = tr.GetStart().AsTime(), tr.GetEnd().AsTime()
		return nil
	}

	result := &invoice_iface.OverviewResponse{
		Data: make([]*invoice_iface.OverviewDataItem, 0, len(pay.MetricType)),
	}

	for _, metric := range pay.MetricType {
		item := &invoice_iface.OverviewDataItem{}

		switch metric {
		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PAYABLE:
			v, err := sumCol(scope(db.Model(&invoice_models.TeamBalance{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)), "balance")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_Payable{Payable: math.Abs(v)}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_RECEIVABLE:
			v, err := sumCol(scope(db.Model(&invoice_models.TeamBalance{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)), "balance")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_Receivable{Receivable: v}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PENDING_PAYMENT:
			// Outgoing in-flight payments live on the payable side; summing one
			// side avoids double-counting the pair.
			v, err := sumCol(scope(db.Model(&invoice_models.TeamBalance{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)), "pending_payment_amount")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_PendingPayment{PendingPayment: v}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_INCOMING_PAYMENT:
			// Incoming in-flight payments live on the receivable side; summing one
			// side avoids double-counting the pair.
			v, err := sumCol(scope(db.Model(&invoice_models.TeamBalance{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)), "pending_payment_amount")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_IncomingPayment{IncomingPayment: v}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_PAYMENT:
			if err := timeWindow(); err != nil {
				return nil, err
			}
			v, err := sumCol(scope(db.Model(&invoice_models.InvoicePayment{}).
				Where("status = ?", invoice_iface.PaymentStatus_PAYMENT_STATUS_ACCEPTED).
				Where("accepted_at BETWEEN ? AND ?", start, end)), "amount")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_TotalPayment{TotalPayment: v}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_PAYABLE:
			if err := timeWindow(); err != nil {
				return nil, err
			}
			v, err := sumCol(scope(db.Model(&invoice_models.BalanceChangeLog{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE).
				Where("created_at BETWEEN ? AND ?", start, end)), "change_amount")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_TotalPayable{TotalPayable: math.Abs(v)}

		case invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_RECEIVABLE:
			if err := timeWindow(); err != nil {
				return nil, err
			}
			v, err := sumCol(scope(db.Model(&invoice_models.BalanceChangeLog{}).
				Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE).
				Where("created_at BETWEEN ? AND ?", start, end)), "change_amount")
			if err != nil {
				return nil, err
			}
			item.Data = &invoice_iface.OverviewDataItem_TotalReceivable{TotalReceivable: v}

		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid metric type"))
		}

		result.Data = append(result.Data, item)
	}

	return connect.NewResponse(result), nil
}
