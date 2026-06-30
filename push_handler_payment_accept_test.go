package invoice_service_test

import (
	"testing"
	"time"

	"github.com/pdcgo/event_source/event_source_mock"
	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/san_collection/san_config"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/schema/services/selling_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestInvoicePushHandlerPaymentAccept covers the invoice-selling-sub PaymentAccept arm:
// an accepted payment submission settles the payer's PAYABLE in the v2 ledger by the
// submission amount (derived from the submission's linked invoices). A redelivery with
// the same message id is a no-op (exactly-once).
func TestInvoicePushHandlerPaymentAccept(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "invoice push handler payment accept",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.Invoice{},
					&db_models.PaymentSubmission{},
					&db_models.PSubmissionInv{},
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalanceDailyLog{},
					&invoice_models.InvoiceExactlyOnceLog{},
				))

				now := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)

				// Starting debt: team 1 owes team 2 10,000 -> PAYABLE(1,2) = -10000.
				assert.NoError(t, invoice_v2.PostBalanceLog(
					db, 2, 1,
					invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE,
					10000, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					"seed", 0, now,
				))

				// A submission (id 70) for that debt: invoice 1 (team 1 -> team 2, 10,000),
				// linked through invoice_payment_submission.
				assert.NoError(t, db.Create(&db_models.Invoice{
					ID:         1,
					FromTeamID: 1,
					ToTeamID:   2,
					Status:     db_models.InvoiceNotPaid,
					Amount:     10000,
					Type:       db_models.InvoProductType,
					Created:    now,
				}).Error)
				assert.NoError(t, db.Create(&db_models.PaymentSubmission{
					ID:        70,
					Status:    db_models.PaymentSubmissionStatusAccepted,
					Amount:    10000,
					CreatedAt: now,
				}).Error)
				assert.NoError(t, db.Create(&db_models.PSubmissionInv{
					InvoiceID:           1,
					PaymentSubmissionID: 70,
				}).Error)

				projectCfg := &san_config.ProjectConfig{ProjectID: "test"}
				handler := invoice_service.NewInvoicePushHandler(db, projectCfg)
				sellingSub := projectCfg.PubsubSubscriberPath("invoice-selling-sub")

				accept := &selling_iface.SellingEvent{
					Data: &selling_iface.SellingEvent_PaymentAccept{
						PaymentAccept: &selling_iface.PaymentAccept{SubmissionId: 70},
					},
				}
				pushID := func(id string) error {
					msg := event_source_mock.NewMockEvent(t, accept)
					msg.Subscription = sellingSub
					msg.Message.MessageID = id
					return handler(t.Context(), msg)
				}
				balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) invoice_models.TeamBalance {
					var b invoice_models.TeamBalance
					assert.NoError(t, db.Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).Limit(1).Find(&b).Error)
					return b
				}
				paymentLogCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&invoice_models.BalanceChangeLog{}).
						Where("team_id = ? AND for_team_id = ? AND change_type = ?",
							uint64(1), uint64(2), invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PAYMENT).
						Count(&n).Error)
					return n
				}
				dedupCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&invoice_models.InvoiceExactlyOnceLog{}).Count(&n).Error)
					return n
				}

				t.Run("accept settles the payer's payable to zero", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))

					// The 10,000 debt is fully settled on both sides.
					assert.Equal(t, float64(0), balanceOf(1, 2, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE).Balance)
					assert.Equal(t, float64(0), balanceOf(2, 1, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE).Balance)
					// A PAYMENT entry was posted for the (1,2) pair.
					assert.Equal(t, int64(1), paymentLogCount())
					assert.Equal(t, int64(1), dedupCount())
				})

				t.Run("redelivery with the same message id is a no-op", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))

					// Balances unchanged (not over-settled), no extra PAYMENT log, one inbox row.
					assert.Equal(t, float64(0), balanceOf(1, 2, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE).Balance)
					assert.Equal(t, int64(1), paymentLogCount())
					assert.Equal(t, int64(1), dedupCount())
				})
			})
		},
	)
}
