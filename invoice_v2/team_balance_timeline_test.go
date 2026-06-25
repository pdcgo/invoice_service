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

func TestTeamBalanceTimeline(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "team balance timeline",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
					&invoice_models.InvoicePayment{},
				))

				adj := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT
				// 05:00 UTC == 12:00 Asia/Jakarta, so each row lands unambiguously in its day.
				at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 5, 0, 0, 0, time.UTC) }
				chg := func(forTeam uint64, bt invoice_iface.BalanceType, amount float64, ts time.Time) invoice_models.BalanceChangeLog {
					return invoice_models.BalanceChangeLog{TeamID: 1, ForTeamID: forTeam, BalanceType: bt, ChangeType: adj, ChangeAmount: amount, CreatedByID: 7, CreatedAt: ts}
				}
				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeLog{
					chg(2, payable, -100, at(2026, 4, 15)), // opening (before window)
					chg(2, receivable, 50, at(2026, 4, 15)),
					chg(2, payable, -30, at(2026, 5, 10)), // May
					chg(2, receivable, 40, at(2026, 5, 12)),
					chg(3, payable, -20, at(2026, 5, 20)),
					chg(2, payable, -10, at(2026, 6, 5)), // June
					chg(2, receivable, 5, at(2026, 6, 7)),
					chg(2, payable, -999, at(2026, 7, 2)), // out of window
				}).Error)

				ptr := func(t time.Time) *time.Time { return &t }
				assert.NoError(t, tx.Create(&[]invoice_models.InvoicePayment{
					{TeamID: 1, ForTeamID: 2, Amount: 25, Status: accepted, AcceptedAt: ptr(at(2026, 5, 15)), CreatedByID: 7, CreatedAt: at(2026, 5, 15)},
					{TeamID: 1, ForTeamID: 2, Amount: 15, Status: accepted, AcceptedAt: ptr(at(2026, 6, 9)), CreatedByID: 7, CreatedAt: at(2026, 6, 9)},
					{TeamID: 1, ForTeamID: 2, Amount: 9, Status: pending, CreatedByID: 7, CreatedAt: at(2026, 6, 9)},                                    // not accepted
					{TeamID: 1, ForTeamID: 2, Amount: 500, Status: accepted, AcceptedAt: ptr(at(2026, 7, 3)), CreatedByID: 7, CreatedAt: at(2026, 7, 3)}, // out of window
				}).Error)

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()
				window := &invoice_iface.TeamBalanceTimelineTimeFilter{
					Start: timestamppb.New(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
					End:   timestamppb.New(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
				}
				// Buckets are Asia/Jakarta (UTC+7, no DST), so a period start is that
				// true instant: UTC midnight of the period minus 7h.
				jkt := func(y int, m time.Month, d int) int64 {
					return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-7 * time.Hour).Unix()
				}
				monthStart := func(m time.Month) int64 { return jkt(2026, m, 1) }

				t.Run("monthly aggregate carries the running balance", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Points, 2)

					may := res.Msg.Points[0]
					assert.Equal(t, monthStart(time.May), may.PeriodStart.AsTime().Unix())
					assert.Equal(t, float64(-150), may.PayableBalance)  // -100 opening + (-30 + -20)
					assert.Equal(t, float64(90), may.ReceivableBalance) // 50 opening + 40
					assert.Equal(t, float64(-50), may.PayableChange)
					assert.Equal(t, float64(40), may.ReceivableChange)
					assert.Equal(t, float64(25), may.TotalPayment)

					jun := res.Msg.Points[1]
					assert.Equal(t, monthStart(time.June), jun.PeriodStart.AsTime().Unix())
					assert.Equal(t, float64(-160), jun.PayableBalance) // carries from May
					assert.Equal(t, float64(95), jun.ReceivableBalance)
					assert.Equal(t, float64(-10), jun.PayableChange)
					assert.Equal(t, float64(5), jun.ReceivableChange)
					assert.Equal(t, float64(15), jun.TotalPayment) // pending + out-of-window excluded
				})

				t.Run("for_team_id narrows to one pair", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1, ForTeamId: 2},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Points, 2)
					assert.Equal(t, float64(-30), res.Msg.Points[0].PayableChange)   // team 3's -20 excluded
					assert.Equal(t, float64(-130), res.Msg.Points[0].PayableBalance) // -100 + -30
				})

				t.Run("daily buckets land on the Jakarta day", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1, ForTeamId: 3},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_DAILY,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Points, 1) // team 3 changed only on May 20
					assert.Equal(t, jkt(2026, time.May, 20), res.Msg.Points[0].PeriodStart.AsTime().Unix())
					assert.Equal(t, float64(-20), res.Msg.Points[0].PayableChange)
					assert.Equal(t, float64(-20), res.Msg.Points[0].PayableBalance)
				})

				t.Run("missing time_range / granularity is invalid", func(t *testing.T) {
					_, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					_, err = svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		})
}
