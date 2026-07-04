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

// allTimelineDataTypes requests every metric, in a fixed order so res.Msg.Data can be
// indexed positionally (Data[0]=common, [1]=payable_balance, … [5]=total_payment).
var allTimelineDataTypes = []invoice_iface.TeamBalanceTimelineDataType{
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_COMMON,
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_PAYABLE_BALANCE,
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_RECEIVABLE_BALANCE,
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_PAYABLE_CHANGE,
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_RECEIVABLE_CHANGE,
	invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_TOTAL_PAYMENT,
}

func TestTeamBalanceTimeline(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "team balance timeline",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
					&invoice_models.InvoicePayment{},
					&invoice_models.TeamBalanceDailyLog{},
				))

				adj := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT
				wfee := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE
				// 05:00 UTC == 12:00 Asia/Jakarta, so each row lands unambiguously in its day.
				at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 5, 0, 0, 0, time.UTC) }
				chg := func(forTeam uint64, bt invoice_iface.BalanceType, amount float64, ts time.Time) invoice_models.BalanceChangeLog {
					return invoice_models.BalanceChangeLog{TeamID: 1, ForTeamID: forTeam, BalanceType: bt, ChangeType: adj, ChangeAmount: amount, CreatedByID: 7, CreatedAt: ts}
				}
				// In-window change_logs drive the buckets + per-change_type breakdown. The two
				// pre-window (April) rows are ignored by the handler (opening comes from the daily
				// rollup seeded below) — kept here to prove they are not double-counted.
				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeLog{
					chg(2, payable, -100, at(2026, 4, 15)), // pre-window (ignored; see daily seed)
					chg(2, receivable, 50, at(2026, 4, 15)),
					chg(2, payable, -30, at(2026, 5, 10)), // May
					chg(2, receivable, 40, at(2026, 5, 12)),
					chg(3, payable, -20, at(2026, 5, 20)),
					// a second change_type in team 3's May-20 bucket → exercises the breakdown.
					{TeamID: 1, ForTeamID: 3, BalanceType: payable, ChangeType: wfee, ChangeAmount: -7, CreatedByID: 7, CreatedAt: at(2026, 5, 20)},
					chg(2, payable, -10, at(2026, 6, 5)), // June
					chg(2, receivable, 5, at(2026, 6, 7)),
					chg(2, payable, -999, at(2026, 7, 2)), // out of window
				}).Error)

				// Opening balance is read from the maintained daily rollup, so seed the pre-window
				// (April) balances there: team 1 owes 100 to team 2 (payable −100) and is owed 50
				// (receivable +50). Day is the Jakarta midnight instant of Apr 15.
				aprDay := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC).Add(-7 * time.Hour)
				assert.NoError(t, tx.Create(&[]invoice_models.TeamBalanceDailyLog{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, Day: aprDay, StartBalance: 0, EndBalance: -100, ChangeAmount: -100, CreatedAt: aprDay, UpdatedAt: aprDay},
					{TeamID: 1, ForTeamID: 2, BalanceType: receivable, Day: aprDay, StartBalance: 0, EndBalance: 50, ChangeAmount: 50, CreatedAt: aprDay, UpdatedAt: aprDay},
				}).Error)

				ptr := func(t time.Time) *time.Time { return &t }
				assert.NoError(t, tx.Create(&[]invoice_models.InvoicePayment{
					{TeamID: 1, ForTeamID: 2, Amount: 25, Status: accepted, AcceptedAt: ptr(at(2026, 5, 15)), CreatedByID: 7, CreatedAt: at(2026, 5, 15)},
					{TeamID: 1, ForTeamID: 2, Amount: 15, Status: accepted, AcceptedAt: ptr(at(2026, 6, 9)), CreatedByID: 7, CreatedAt: at(2026, 6, 9)},
					{TeamID: 1, ForTeamID: 2, Amount: 9, Status: pending, CreatedByID: 7, CreatedAt: at(2026, 6, 9)},                                     // not accepted
					{TeamID: 1, ForTeamID: 2, Amount: 500, Status: accepted, AcceptedAt: ptr(at(2026, 7, 3)), CreatedByID: 7, CreatedAt: at(2026, 7, 3)}, // out of window
				}).Error)

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()
				window := &invoice_iface.TeamBalanceTimelineTimeFilter{
					Start: timestamppb.New(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
					End:   timestamppb.New(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
				}
				// Buckets are Asia/Jakarta (UTC+7, no DST), so a period start is that true
				// instant: UTC midnight of the period minus 7h. The bucket id is that Unix second.
				jkt := func(y int, m time.Month, d int) int64 {
					return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-7 * time.Hour).Unix()
				}
				monthStart := func(m time.Month) int64 { return jkt(2026, m, 1) }

				t.Run("monthly aggregate carries the running balance", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
						DataTypes:   allTimelineDataTypes,
					}))
					assert.NoError(t, err)

					may, jun := uint64(monthStart(time.May)), uint64(monthStart(time.June))
					assert.Equal(t, []uint64{may, jun}, res.Msg.Ids)
					assert.Len(t, res.Msg.Data, 6)

					common := res.Msg.Data[0].GetCommon().GetData()
					assert.Equal(t, monthStart(time.May), common[may].PeriodStart.AsTime().Unix())
					assert.Equal(t, monthStart(time.June), common[jun].PeriodStart.AsTime().Unix())

					pb := res.Msg.Data[1].GetPayableBalance().GetData()
					assert.Equal(t, float64(-157), pb[may].Balance) // -100 opening + (-30 + -20 + -7 fee)
					assert.Equal(t, float64(-167), pb[jun].Balance) // carries from May

					rb := res.Msg.Data[2].GetReceivableBalance().GetData()
					assert.Equal(t, float64(90), rb[may].Balance) // 50 opening + 40
					assert.Equal(t, float64(95), rb[jun].Balance)

					pc := res.Msg.Data[3].GetPayableChange().GetData()
					assert.Equal(t, float64(-57), pc[may].Amount) // -30 + -20 + -7 fee
					assert.Equal(t, float64(-10), pc[jun].Amount)

					rc := res.Msg.Data[4].GetReceivableChange().GetData()
					assert.Equal(t, float64(40), rc[may].Amount)
					assert.Equal(t, float64(5), rc[jun].Amount)

					tp := res.Msg.Data[5].GetTotalPayment().GetData()
					assert.Equal(t, float64(25), tp[may].Amount)
					assert.Equal(t, float64(15), tp[jun].Amount) // pending + out-of-window excluded
				})

				t.Run("for_team_id narrows to one pair", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1, ForTeamId: 2},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
						DataTypes:   allTimelineDataTypes,
					}))
					assert.NoError(t, err)
					may := uint64(monthStart(time.May))
					assert.Len(t, res.Msg.Ids, 2)
					assert.Equal(t, float64(-30), res.Msg.Data[3].GetPayableChange().GetData()[may].Amount)    // team 3's -20 excluded
					assert.Equal(t, float64(-130), res.Msg.Data[1].GetPayableBalance().GetData()[may].Balance) // -100 + -30
				})

				t.Run("daily buckets land on the Jakarta day", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1, ForTeamId: 3},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_DAILY,
						DataTypes:   allTimelineDataTypes,
					}))
					assert.NoError(t, err)
					day := uint64(jkt(2026, time.May, 20))
					assert.Equal(t, []uint64{day}, res.Msg.Ids) // team 3 changed only on May 20
					pcDay := res.Msg.Data[3].GetPayableChange().GetData()[day]
					assert.Equal(t, float64(-27), pcDay.Amount) // -20 adjustment + -7 fee
					assert.Equal(t, float64(-27), res.Msg.Data[1].GetPayableBalance().GetData()[day].Balance)

					// the change carries a per-BalanceChangeType breakdown; amount == sum of change.
					byType := map[invoice_iface.BalanceChangeType]*invoice_iface.ChangeSumAmount{}
					var breakSum float64
					for _, c := range pcDay.Change {
						byType[c.ChangeType] = c
						breakSum += c.Amount
					}
					assert.Equal(t, pcDay.Amount, breakSum)
					assert.Len(t, pcDay.Change, 2)
					assert.Equal(t, float64(-20), byType[adj].Amount)
					assert.Equal(t, int64(1), byType[adj].TransactionCount)
					assert.Equal(t, float64(-7), byType[wfee].Amount)
					assert.Equal(t, int64(1), byType[wfee].TransactionCount)
				})

				t.Run("data_types selects which metrics are returned", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
						DataTypes: []invoice_iface.TeamBalanceTimelineDataType{
							invoice_iface.TeamBalanceTimelineDataType_TEAM_BALANCE_TIMELINE_DATA_TYPE_PAYABLE_BALANCE,
						},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Data, 1)
					assert.NotNil(t, res.Msg.Data[0].GetPayableBalance()) // only the requested metric
					assert.Nil(t, res.Msg.Data[0].GetCommon())
					assert.Len(t, res.Msg.Ids, 2)
				})

				t.Run("descending sort reverses the ids", func(t *testing.T) {
					res, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
						Sort: &invoice_iface.TeamBalanceTimelineSort{
							SortType: invoice_iface.SortType_SORT_TYPE_DESC,
							S:        &invoice_iface.TeamBalanceTimelineSort_Common{Common: invoice_iface.TeamBalanceTimelineCommonSort_TEAM_BALANCE_TIMELINE_COMMON_SORT_PERIOD},
						},
						DataTypes: allTimelineDataTypes,
					}))
					assert.NoError(t, err)
					may, jun := uint64(monthStart(time.May)), uint64(monthStart(time.June))
					assert.Equal(t, []uint64{jun, may}, res.Msg.Ids) // reversed
					// balances are still the end-of-period running values, keyed by id.
					pb := res.Msg.Data[1].GetPayableBalance().GetData()
					assert.Equal(t, float64(-157), pb[may].Balance)
					assert.Equal(t, float64(-167), pb[jun].Balance)
				})

				t.Run("missing time_range / granularity / data_types is invalid", func(t *testing.T) {
					_, err := svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
						DataTypes:   allTimelineDataTypes,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err)) // no time_range

					_, err = svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						DataTypes: allTimelineDataTypes,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err)) // no granularity

					_, err = svc.TeamBalanceTimeline(ctx, connect.NewRequest(&invoice_iface.TeamBalanceTimelineRequest{
						TimeRange:   window,
						Filter:      &invoice_iface.TeamBalanceTimelineFilter{TeamId: 1},
						Granularity: invoice_iface.TimeGranularity_TIME_GRANULARITY_MONTHLY,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err)) // no data_types
				})
			})
		})
}
