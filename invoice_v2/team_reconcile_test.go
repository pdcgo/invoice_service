package invoice_v2_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	role_base "github.com/pdcgo/schema/services/role_base/v1"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/pdcgo/user_service/access_interceptors"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestTeamReconcile(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "team reconcile",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&db_models.Invoice{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalance{},
					&invoice_models.TeamBalanceDailyLog{},
				))

				svc := invoice_v2.NewInvoiceService(tx)
				// TeamReconcile -> CreateBalanceLog reads the caller from context
				// (normally set by the access interceptor); inject it directly since
				// the handler runs outside the interceptor in tests.
				ctx := access_interceptors.SetIdentityToCtx(
					context.Background(),
					&role_base.Identity{IdentityId: uint32(callerID)},
				)

				seedInvoice := func(from, to uint, amount float64, status db_models.InvoiceStatus) {
					assert.NoError(t, tx.Create(&db_models.Invoice{
						FromTeamID: from,
						ToTeamID:   to,
						Amount:     amount,
						Status:     status,
					}).Error)
				}

				balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalance, bool) {
					var bal invoice_models.TeamBalance
					res := tx.
						Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).
						Limit(1).
						Find(&bal)
					assert.NoError(t, res.Error)
					return bal, res.RowsAffected > 0
				}

				logCount := func() int64 {
					var n int64
					assert.NoError(t, tx.Model(&invoice_models.BalanceChangeLog{}).Count(&n).Error)
					return n
				}

				reconcile := func(teamID uint64) error {
					_, err := svc.TeamReconcile(ctx, connect.NewRequest(&invoice_iface.TeamReconcileRequest{
						TeamId: teamID,
					}))
					return err
				}

				t.Run("under-stated balance is raised to the legacy total", func(t *testing.T) {
					// from=10 owes 60+40 to to=20; a paid invoice must be ignored.
					seedInvoice(10, 20, 60, db_models.InvoiceNotPaid)
					seedInvoice(10, 20, 40, db_models.InvoiceNotPaid)
					seedInvoice(10, 20, 999, db_models.InvoicePaid)

					assert.NoError(t, reconcile(10))

					pay, ok := balanceOf(10, 20, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(-100), pay.Balance)

					rec, ok := balanceOf(20, 10, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.True(t, ok)
					assert.Equal(t, float64(100), rec.Balance)
				})

				t.Run("over-stated balance is lowered to the legacy total", func(t *testing.T) {
					// legacy truth: from=11 owes 100 to to=21.
					seedInvoice(11, 21, 100, db_models.InvoiceNotPaid)
					// but the new system already overstates the debt at 150 (PAYABLE(11,21) = -150).
					_, err := svc.CreateBalanceLog(ctx, connect.NewRequest(&invoice_iface.CreateBalanceLogRequest{
						TeamId:       21,
						ForTeamId:    11,
						ChangeType:   invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						ChangeAmount: 150,
						BalanceType:  invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					}))
					assert.NoError(t, err)

					assert.NoError(t, reconcile(11))

					pay, _ := balanceOf(11, 21, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.Equal(t, float64(-100), pay.Balance)
					rec, _ := balanceOf(21, 11, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE)
					assert.Equal(t, float64(100), rec.Balance)
				})

				t.Run("pairs not involving the team are untouched", func(t *testing.T) {
					seedInvoice(30, 40, 70, db_models.InvoiceNotPaid)

					assert.NoError(t, reconcile(11)) // reconciling team 11 must not touch 30/40

					_, ok := balanceOf(30, 40, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.False(t, ok)
				})

				t.Run("re-running is idempotent (diff is zero)", func(t *testing.T) {
					before := logCount()
					assert.NoError(t, reconcile(10))
					assert.Equal(t, before, logCount(), "no new ledger entries on a converged team")

					pay, _ := balanceOf(10, 20, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE)
					assert.Equal(t, float64(-100), pay.Balance)
				})
			})
		},
	)
}
