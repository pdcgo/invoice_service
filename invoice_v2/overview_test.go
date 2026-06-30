package invoice_v2_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

func TestOverview(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "overview",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.InvoicePayment{},
				))

				inWindow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				before := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
				after := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
				ptr := func(t time.Time) *time.Time { return &t }

				// team 1 balances: owes 100 (pending 30) to t2 and 40 to t3; is owed 70 (incoming
				// pending 15) by t2 and 5 (incoming pending 10) by t3.
				assert.NoError(t, tx.Create(&[]invoice_models.TeamBalance{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, Balance: -100, PendingPaymentAmount: 30},
					{TeamID: 1, ForTeamID: 3, BalanceType: payable, Balance: -40, PendingPaymentAmount: 0},
					{TeamID: 1, ForTeamID: 2, BalanceType: receivable, Balance: 70, PendingPaymentAmount: 15},
					{TeamID: 1, ForTeamID: 3, BalanceType: receivable, Balance: 5, PendingPaymentAmount: 10},
					{TeamID: 9, ForTeamID: 2, BalanceType: payable, Balance: -999, PendingPaymentAmount: 5}, // other team, excluded
				}).Error)

				// change log: -100 + -40 payable and +70 receivable in-window; a -500 payable out-of-window.
				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeLog{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, ChangeAmount: -100, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 3, BalanceType: payable, ChangeAmount: -40, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, BalanceType: receivable, ChangeAmount: 70, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, ChangeAmount: -500, CreatedAt: before}, // excluded
				}).Error)

				// payments: 25 accepted in-window counts; pending and out-of-window do not.
				assert.NoError(t, tx.Create(&[]invoice_models.InvoicePayment{
					{TeamID: 1, ForTeamID: 2, Amount: 25, Status: accepted, AcceptedAt: ptr(inWindow), CreatedByID: 7, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, Amount: 9, Status: pending, CreatedByID: 7, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, Amount: 200, Status: accepted, AcceptedAt: ptr(after), CreatedByID: 7, CreatedAt: after},
				}).Error)

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()
				window := &invoice_iface.OverviewTimeFilter{
					Start: timestamppb.New(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
					End:   timestamppb.New(time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC)),
				}

				t.Run("all metrics, in requested order, payable positive, windows respected", func(t *testing.T) {
					res, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						TimeRange: window,
						Filter:    &invoice_iface.OverviewFilter{TeamId: 1},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PAYABLE,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_RECEIVABLE,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PENDING_PAYMENT,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_INCOMING_PAYMENT,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_PAYMENT,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_PAYABLE,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_RECEIVABLE,
						},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Data, 7)
					assert.Equal(t, float64(140), res.Msg.Data[0].GetPayable())
					assert.Equal(t, float64(75), res.Msg.Data[1].GetReceivable())
					assert.Equal(t, float64(30), res.Msg.Data[2].GetPendingPayment())
					assert.Equal(t, float64(25), res.Msg.Data[3].GetIncomingPayment()) // receivable-side pending: 15 + 10
					assert.Equal(t, float64(25), res.Msg.Data[4].GetTotalPayment())
					assert.Equal(t, float64(140), res.Msg.Data[5].GetTotalPayable())
					assert.Equal(t, float64(70), res.Msg.Data[6].GetTotalReceivable())
				})

				t.Run("order follows the requested metric list", func(t *testing.T) {
					res, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						Filter: &invoice_iface.OverviewFilter{TeamId: 1},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_RECEIVABLE,
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PAYABLE,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, float64(75), res.Msg.Data[0].GetReceivable())
					assert.Equal(t, float64(140), res.Msg.Data[1].GetPayable())
				})

				t.Run("for_team_id narrows the scope", func(t *testing.T) {
					res, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						Filter: &invoice_iface.OverviewFilter{TeamId: 1, ForTeamId: 3},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PAYABLE,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, float64(40), res.Msg.Data[0].GetPayable())
				})

				t.Run("incoming payment is receivable-side pending, scoped per counterparty", func(t *testing.T) {
					res, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						Filter: &invoice_iface.OverviewFilter{TeamId: 1, ForTeamId: 3},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_INCOMING_PAYMENT,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, float64(10), res.Msg.Data[0].GetIncomingPayment())
				})

				t.Run("windowed metric without time_range is invalid", func(t *testing.T) {
					_, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						Filter: &invoice_iface.OverviewFilter{TeamId: 1},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_TOTAL_PAYMENT,
						},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("current metric without time_range is fine", func(t *testing.T) {
					res, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						Filter: &invoice_iface.OverviewFilter{TeamId: 1},
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_PAYABLE,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, float64(140), res.Msg.Data[0].GetPayable())
				})

				t.Run("unspecified metric is invalid", func(t *testing.T) {
					_, err := svc.Overview(ctx, connect.NewRequest(&invoice_iface.OverviewRequest{
						MetricType: []invoice_iface.OverviewMetricType{
							invoice_iface.OverviewMetricType_OVERVIEW_METRIC_TYPE_UNSPECIFIED,
						},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		})
}
