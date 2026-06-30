package invoice_v2_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	role_base "github.com/pdcgo/schema/services/role_base/v1"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/pdcgo/user_service/access_interceptors"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const (
	payable    = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
	receivable = invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
	pending    = invoice_iface.PaymentStatus_PAYMENT_STATUS_PENDING
	accepted   = invoice_iface.PaymentStatus_PAYMENT_STATUS_ACCEPTED
	rejected   = invoice_iface.PaymentStatus_PAYMENT_STATUS_REJECTED
)

func TestPayment(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "payment",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.InvoicePayment{},
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalanceDailyLog{},
				))

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := access_interceptors.SetIdentityToCtx(
					context.Background(),
					&role_base.Identity{IdentityId: 7},
				)

				balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalance, bool) {
					var b invoice_models.TeamBalance
					res := tx.Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).Limit(1).Find(&b)
					assert.NoError(t, res.Error)
					return b, res.RowsAffected > 0
				}

				create := func(payer, receiver uint64) uint64 {
					res, err := svc.CreatePayment(ctx, connect.NewRequest(&invoice_iface.CreatePaymentRequest{
						TeamId:     payer,
						ForTeamId:  receiver,
						Amount:     30,
						Note:       "n",
						DocumentId: "doc",
					}))
					assert.NoError(t, err)
					return res.Msg.Id
				}

				t.Run("create: pending bumped on both sides, balances zero", func(t *testing.T) {
					id := create(1, 2)
					assert.NotZero(t, id)

					pyb, ok := balanceOf(1, 2, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(30), pyb.PendingPaymentAmount)
					assert.Equal(t, float64(0), pyb.Balance)

					rcv, ok := balanceOf(2, 1, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(30), rcv.PendingPaymentAmount)
					assert.Equal(t, float64(0), rcv.Balance)

					var p invoice_models.InvoicePayment
					assert.NoError(t, tx.First(&p, id).Error)
					assert.Equal(t, pending, p.Status)
					assert.Equal(t, uint64(7), p.CreatedByID)
				})

				t.Run("accept: settles balance, clears pending, marks accepted", func(t *testing.T) {
					// seed: payer 3 owes receiver 4 by 30 -> PAYABLE(3,4) = -30.
					_, err := svc.CreateBalanceLog(ctx, connect.NewRequest(&invoice_iface.CreateBalanceLogRequest{
						TeamId:       4,
						ForTeamId:    3,
						ChangeType:   invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						ChangeAmount: 30,
						BalanceType:  receivable,
					}))
					assert.NoError(t, err)

					id := create(3, 4)
					_, err = svc.AcceptPayment(ctx, connect.NewRequest(&invoice_iface.AcceptPaymentRequest{
						TeamId: 3, ForTeamId: 4, PaymentId: id,
					}))
					assert.NoError(t, err)

					// debt of 30 fully settled to zero on both sides.
					pyb, _ := balanceOf(3, 4, payable)
					assert.Equal(t, float64(0), pyb.Balance)
					assert.Equal(t, float64(0), pyb.PendingPaymentAmount)

					rcv, _ := balanceOf(4, 3, receivable)
					assert.Equal(t, float64(0), rcv.Balance)
					assert.Equal(t, float64(0), rcv.PendingPaymentAmount)

					var logs int64
					assert.NoError(t, tx.Model(&invoice_models.BalanceChangeLog{}).
						Where("team_id = ? AND change_type = ?", uint64(3), invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PAYMENT).
						Count(&logs).Error)
					assert.Equal(t, int64(1), logs)

					var p invoice_models.InvoicePayment
					assert.NoError(t, tx.First(&p, id).Error)
					assert.Equal(t, accepted, p.Status)
					assert.NotNil(t, p.AcceptedAt)
					assert.NotNil(t, p.CompletedByID)
					assert.Equal(t, uint64(7), *p.CompletedByID)

					t.Run("double-accept rejected", func(t *testing.T) {
						_, err := svc.AcceptPayment(ctx, connect.NewRequest(&invoice_iface.AcceptPaymentRequest{
							TeamId: 3, ForTeamId: 4, PaymentId: id,
						}))
						assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					})
				})

				t.Run("accept: overpayment recorded as credit", func(t *testing.T) {
					// seed: payer 13 owes receiver 14 by 30 -> PAYABLE(13,14) = -30.
					_, err := svc.CreateBalanceLog(ctx, connect.NewRequest(&invoice_iface.CreateBalanceLogRequest{
						TeamId:       14,
						ForTeamId:    13,
						ChangeType:   invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						ChangeAmount: 30,
						BalanceType:  receivable,
					}))
					assert.NoError(t, err)

					// overpay: 100 against a 30 debt.
					res, err := svc.CreatePayment(ctx, connect.NewRequest(&invoice_iface.CreatePaymentRequest{
						TeamId:    13,
						ForTeamId: 14,
						Amount:    100,
						Note:      "n",
					}))
					assert.NoError(t, err)

					_, err = svc.AcceptPayment(ctx, connect.NewRequest(&invoice_iface.AcceptPaymentRequest{
						TeamId: 13, ForTeamId: 14, PaymentId: res.Msg.Id,
					}))
					assert.NoError(t, err)

					// the 30 debt is fully settled.
					pyb, _ := balanceOf(13, 14, payable)
					assert.Equal(t, float64(0), pyb.Balance)
					assert.Equal(t, float64(0), pyb.PendingPaymentAmount)
					rcvDebt, _ := balanceOf(14, 13, receivable)
					assert.Equal(t, float64(0), rcvDebt.Balance)
					assert.Equal(t, float64(0), rcvDebt.PendingPaymentAmount)

					// the 70 surplus is a clean credit: payer 13 is now owed 70 by 14.
					credit, ok := balanceOf(13, 14, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(70), credit.Balance)
					mirror, ok := balanceOf(14, 13, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(-70), mirror.Balance)
				})

				t.Run("reject: clears pending, balances untouched, marks rejected", func(t *testing.T) {
					id := create(5, 6)
					_, err := svc.RejectPayment(ctx, connect.NewRequest(&invoice_iface.RejectPaymentRequest{
						TeamId: 5, ForTeamId: 6, PaymentId: id,
					}))
					assert.NoError(t, err)

					pyb, _ := balanceOf(5, 6, payable)
					assert.Equal(t, float64(0), pyb.Balance)
					assert.Equal(t, float64(0), pyb.PendingPaymentAmount)

					var p invoice_models.InvoicePayment
					assert.NoError(t, tx.First(&p, id).Error)
					assert.Equal(t, rejected, p.Status)
					assert.NotNil(t, p.RejectedAt)

					t.Run("double-reject rejected", func(t *testing.T) {
						_, err := svc.RejectPayment(ctx, connect.NewRequest(&invoice_iface.RejectPaymentRequest{
							TeamId: 5, ForTeamId: 6, PaymentId: id,
						}))
						assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					})
				})

				t.Run("accept with wrong pair rejected", func(t *testing.T) {
					id := create(7, 8)
					_, err := svc.AcceptPayment(ctx, connect.NewRequest(&invoice_iface.AcceptPaymentRequest{
						TeamId: 7, ForTeamId: 999, PaymentId: id,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("list outgoing and incoming", func(t *testing.T) {
					create(10, 11)
					create(10, 12)

					out, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId: 10,
						Page:   &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, out.Msg.Payments, 2)
					assert.Equal(t, int64(2), out.Msg.PageInfo.TotalItems)

					// counterparty filter
					filtered, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId:    10,
						ForTeamId: 11,
						Page:      &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, filtered.Msg.Payments, 1)

					// receiver 11 sees the incoming payment
					inc, err := svc.ListIncomingPayment(ctx, connect.NewRequest(&invoice_iface.ListIncomingPaymentRequest{
						ForTeamId: 11,
						Page:      &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, inc.Msg.Payments, 1)
					assert.Equal(t, uint64(10), inc.Msg.Payments[0].TeamId)
				})

				t.Run("list filtered by created_at window", func(t *testing.T) {
					// Seed three payments (payer 20 -> receiver 21) at known times by
					// inserting directly so created_at is controlled (CreatePayment uses now()).
					now := time.Now()
					for _, ts := range []time.Time{
						now.Add(-72 * time.Hour), // d-3
						now.Add(-48 * time.Hour), // d-2
						now,                      // today
					} {
						assert.NoError(t, tx.Create(&invoice_models.InvoicePayment{
							TeamID:      20,
							ForTeamID:   21,
							Amount:      5,
							Status:      pending,
							CreatedByID: 7,
							CreatedAt:   ts,
						}).Error)
					}

					page := &common.PageFilter{Page: 1, Limit: 10}

					// No window -> all three.
					all, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId: 20, Page: page,
					}))
					assert.NoError(t, err)
					assert.Len(t, all.Msg.Payments, 3)

					// from = 60h ago -> the d-2 and today rows (2).
					fromOnly, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId:   20,
						Page:     page,
						FromTime: timestamppb.New(now.Add(-60 * time.Hour)),
					}))
					assert.NoError(t, err)
					assert.Len(t, fromOnly.Msg.Payments, 2)

					// to = 60h ago -> only the d-3 row (1).
					toOnly, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId: 20,
						Page:   page,
						ToTime: timestamppb.New(now.Add(-60 * time.Hour)),
					}))
					assert.NoError(t, err)
					assert.Len(t, toOnly.Msg.Payments, 1)

					// window [60h ago, 36h ago] -> only the d-2 row (1).
					window, err := svc.ListPayment(ctx, connect.NewRequest(&invoice_iface.ListPaymentRequest{
						TeamId:   20,
						Page:     page,
						FromTime: timestamppb.New(now.Add(-60 * time.Hour)),
						ToTime:   timestamppb.New(now.Add(-36 * time.Hour)),
					}))
					assert.NoError(t, err)
					assert.Len(t, window.Msg.Payments, 1)

					// Incoming side honors the same window (receiver 21).
					inc, err := svc.ListIncomingPayment(ctx, connect.NewRequest(&invoice_iface.ListIncomingPaymentRequest{
						ForTeamId: 21,
						Page:      page,
						FromTime:  timestamppb.New(now.Add(-60 * time.Hour)),
					}))
					assert.NoError(t, err)
					assert.Len(t, inc.Msg.Payments, 2)
				})
			})
		},
	)
}
