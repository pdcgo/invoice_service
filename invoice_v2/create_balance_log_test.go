package invoice_v2_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	role_base "github.com/pdcgo/schema/services/role_base/v1"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/pdcgo/user_service/access_interceptors"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

const callerID uint64 = 7

func TestCreateBalanceLog(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "create balance log",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalance{},
					&invoice_models.TeamBalanceDailyLog{},
				))

				svc := invoice_v2.NewInvoiceService(tx)
				// CreateBalanceLog reads the caller from context (normally set by the
				// access interceptor); inject it directly since the handler runs
				// outside the interceptor in tests.
				ctx := access_interceptors.SetIdentityToCtx(
					context.Background(),
					&role_base.Identity{IdentityId: uint32(callerID)},
				)

				balanceOf := func(t *testing.T, teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalance, bool) {
					var bal invoice_models.TeamBalance
					res := tx.
						Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).
						Limit(1).
						Find(&bal)
					assert.NoError(t, res.Error)
					return bal, res.RowsAffected > 0
				}

				dailyOf := func(t *testing.T, teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalanceDailyLog, bool) {
					var d invoice_models.TeamBalanceDailyLog
					res := tx.
						Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).
						Limit(1).
						Find(&d)
					assert.NoError(t, res.Error)
					return d, res.RowsAffected > 0
				}

				count := func(t *testing.T, model interface{}) int64 {
					var n int64
					assert.NoError(t, tx.Model(model).Count(&n).Error)
					return n
				}

				post := func() error {
					_, err := svc.CreateBalanceLog(ctx, connect.NewRequest(&invoice_iface.CreateBalanceLogRequest{
						TeamId:       1,
						ForTeamId:    2,
						ChangeType:   invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						ChangeAmount: 30,
						BalanceType:  invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
						Note:         "first",
					}))
					return err
				}

				t.Run("first post writes the double entry", func(t *testing.T) {
					assert.NoError(t, post())

					rec, ok := balanceOf(t, 1, 2, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(30), rec.Balance)

					pay, ok := balanceOf(t, 2, 1, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(-30), pay.Balance)

					assert.Equal(t, int64(2), count(t, &invoice_models.BalanceChangeLog{}))
					assert.Equal(t, int64(2), count(t, &invoice_models.TeamBalanceDailyLog{}))

					// both ledger legs are stamped with the caller id.
					var logs []invoice_models.BalanceChangeLog
					assert.NoError(t, tx.Find(&logs).Error)
					for _, l := range logs {
						assert.Equal(t, callerID, l.CreatedByID)
					}

					recDaily, ok := dailyOf(t, 1, 2, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(0), recDaily.StartBalance)
					assert.Equal(t, float64(30), recDaily.EndBalance)
					assert.Equal(t, float64(30), recDaily.ChangeAmount)

					payDaily, ok := dailyOf(t, 2, 1, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(0), payDaily.StartBalance)
					assert.Equal(t, float64(-30), payDaily.EndBalance)
					assert.Equal(t, float64(-30), payDaily.ChangeAmount)
				})

				t.Run("second post accumulates within the same day", func(t *testing.T) {
					assert.NoError(t, post())

					rec, _ := balanceOf(t, 1, 2, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.Equal(t, float64(60), rec.Balance)

					pay, _ := balanceOf(t, 2, 1, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.Equal(t, float64(-60), pay.Balance)

					// ledger grows; daily rows stay at 2 (same day) and accumulate.
					assert.Equal(t, int64(4), count(t, &invoice_models.BalanceChangeLog{}))
					assert.Equal(t, int64(2), count(t, &invoice_models.TeamBalanceDailyLog{}))

					recDaily, _ := dailyOf(t, 1, 2, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.Equal(t, float64(0), recDaily.StartBalance)
					assert.Equal(t, float64(60), recDaily.EndBalance)
					assert.Equal(t, float64(60), recDaily.ChangeAmount)

					payDaily, _ := dailyOf(t, 2, 1, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.Equal(t, float64(0), payDaily.StartBalance)
					assert.Equal(t, float64(-60), payDaily.EndBalance)
					assert.Equal(t, float64(-60), payDaily.ChangeAmount)
				})

				t.Run("same team rejected", func(t *testing.T) {
					_, err := svc.CreateBalanceLog(ctx, connect.NewRequest(&invoice_iface.CreateBalanceLogRequest{
						TeamId:       1,
						ForTeamId:    1,
						ChangeType:   invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						ChangeAmount: 30,
						BalanceType:  invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}
