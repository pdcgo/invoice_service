package invoice_mutations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pdcgo/invoice_service/invoice_mutations"
	"github.com/pdcgo/invoice_service/invoice_query"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/identity/mock_identity"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestPaymentSubmission(t *testing.T) {
	var db gorm.DB

	seedInvoice := func(orderID uint, status db_models.InvoiceStatus, amount float64) *db_models.Invoice {
		inv := db_models.Invoice{
			OrderID:    &orderID,
			FromTeamID: 1,
			ToTeamID:   2,
			Status:     status,
			Amount:     amount,
			Type:       db_models.InvoProductType,
			Created:    time.Now(),
		}
		err := db.Create(&inv).Error
		assert.Nil(t, err)

		return &inv
	}

	moretest.Suite(
		t,
		"test payment submission",
		moretest.SetupListFunc{
			moretest_mock.MockSqliteDatabase(&db),
			func(t *testing.T) func() error {
				err := db.AutoMigrate(
					&db_models.Invoice{},
					&db_models.PaymentSubmission{},
					&db_models.PSubmissionInv{},
					&db_models.PaymentSubmissionLog{},
					&db_models.PaymentHistory{},
				)
				assert.Nil(t, err)

				return nil
			},
		},
		func(t *testing.T) {
			agent := mock_identity.NewMockAgent(1, "mock")

			invoice1 := seedInvoice(1, db_models.InvoiceNotPaid, 15_000)

			t.Run("test create submission", func(t *testing.T) {
				t.Run("test create with amount", func(t *testing.T) {
					db.Transaction(func(tx *gorm.DB) error {
						service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)

						invoiceQuery := invoice_query.NewInvoiceQuery(tx, true)
						invoiceQuery.
							FromTeam(1).
							ToTeam(2).
							Status(db_models.InvoiceNotPaid)

						err := service.CreateSubmission(invoiceQuery, "receipt_invoice", 15_000)
						assert.Nil(t, err)

						return errors.New("dummy error")
					})
				})

				t.Run("test create with invoice", func(t *testing.T) {
					db.Transaction(func(tx *gorm.DB) error {
						err := tx.Transaction(func(tx *gorm.DB) error {
							service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)

							invoiceQuery := invoice_query.NewInvoiceQuery(tx, true)
							invoiceQuery.
								FromTeam(1).
								ToTeam(2).
								Status(db_models.InvoiceNotPaid).
								WithIDs([]uint{invoice1.ID})

							err := service.CreateSubmissionFromInvoice(invoiceQuery, "receipt_invoice")
							assert.Nil(t, err)

							return nil
						})
						if err != nil {
							return err
						}

						t.Run("test submission data", func(t *testing.T) {
							invoice := db_models.Invoice{}
							invoiceQuery := invoice_query.NewInvoiceQuery(tx, false)
							err := invoiceQuery.WithIDs([]uint{invoice1.ID}).GetQuery().Find(&invoice).Error
							assert.Nil(t, err)
							assert.NotEmpty(t, invoice)
							assert.True(t, invoice.HasSubmission)
						})

						return errors.New("dummy error")
					})
				})
			})

			t.Run("test accept submission", func(t *testing.T) {
				db.Transaction(func(tx *gorm.DB) error {

					err := tx.Transaction(func(tx *gorm.DB) error {
						service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)
						invoiceQuery := invoice_query.NewInvoiceQuery(tx, true)
						invoiceQuery.
							FromTeam(1).
							ToTeam(2).
							Status(db_models.InvoiceNotPaid)

						err := service.CreateSubmission(invoiceQuery, "receipt_invoice", 15_000)
						assert.Nil(t, err)

						return nil
					})
					if err != nil {
						return err
					}

					t.Run("test accept without adjustment", func(t *testing.T) {
						tx.Transaction(func(tx *gorm.DB) error {
							err = tx.Transaction(func(tx *gorm.DB) error {
								service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)
								submissionQuery := invoice_query.NewPaymentSubmissionQuery(tx, true)

								submissionQuery.
									JoinInvoice("JOIN").
									WithIDs([]uint{invoice1.ID})

								service, err := service.GetFromQuery(submissionQuery)
								if err != nil {
									return err
								}

								err = service.AcceptSubmission()
								assert.Nil(t, err)

								return nil
							})
							if err != nil {
								return err
							}

							t.Run("test data", func(t *testing.T) {
								t.Run("test invoice paid", func(t *testing.T) {
									invoice := db_models.Invoice{}
									invoiceQuery := invoice_query.NewInvoiceQuery(tx, false)
									err := invoiceQuery.WithIDs([]uint{invoice1.ID}).GetQuery().Find(&invoice).Error
									assert.Nil(t, err)
									assert.NotEmpty(t, invoice)
									assert.True(t, invoice.HasSubmission)
									assert.Equal(t, db_models.InvoicePaid, invoice.Status)
									assert.NotEmpty(t, invoice.PaidAt)
								})

								t.Run("tets submission accepted", func(t *testing.T) {
									submission := &db_models.PaymentSubmission{}
									submissionQuery := invoice_query.NewPaymentSubmissionQuery(tx, false)
									err := submissionQuery.JoinInvoice("JOIN").WithIDs([]uint{invoice1.ID}).GetQuery().Find(&submission).Error
									assert.Nil(t, err)
									assert.NotEmpty(t, submission)
									assert.Equal(t, db_models.PaymentSubmissionStatusAccepted, submission.Status)
								})

								t.Run("test payment histories", func(t *testing.T) {
									paymentHistory := db_models.PaymentHistory{}
									invoiceQuery := invoice_query.NewPaymentHistoryQuery(tx.Debug(), false)
									err := invoiceQuery.JoinInvoice("JOIN").WithIDs([]uint{invoice1.ID}).GetQuery().Find(&paymentHistory).Error
									assert.Nil(t, err)
									assert.NotEmpty(t, paymentHistory)
								})
							})

							return errors.New("dummy error")
						})
					})

					t.Run("test accept with adjustment", func(t *testing.T) {
						err = tx.Model(&db_models.Invoice{}).Where("id = ?", invoice1.ID).Update("need_adj", true).Error
						assert.Nil(t, err)

						err = tx.Transaction(func(tx *gorm.DB) error {
							service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)
							submissionQuery := invoice_query.NewPaymentSubmissionQuery(tx, true)

							submissionQuery.
								JoinInvoice("JOIN").
								WithIDs([]uint{invoice1.ID})

							service, err := service.GetFromQuery(submissionQuery)
							if err != nil {
								return err
							}

							err = service.AcceptSubmission()
							assert.Nil(t, err)

							return nil
						})
						assert.Nil(t, err)
						t.Run("test data adjustment invoice", func(t *testing.T) {
							invoice := db_models.Invoice{}
							invoiceQuery := invoice_query.NewInvoiceQuery(tx, false)
							err := invoiceQuery.WithType(db_models.InvoProductAdjustment).GetQuery().Find(&invoice).Error
							assert.Nil(t, err)
							assert.NotEmpty(t, invoice)
						})
					})

					return errors.New("dummy error")
				})
			})

			t.Run("test reject submission", func(t *testing.T) {
				db.Transaction(func(tx *gorm.DB) error {

					err := tx.Transaction(func(tx *gorm.DB) error {
						service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)
						invoiceQuery := invoice_query.NewInvoiceQuery(tx, true)
						invoiceQuery.
							FromTeam(1).
							ToTeam(2).
							Status(db_models.InvoiceNotPaid)

						err := service.CreateSubmission(invoiceQuery, "receipt_invoice", 15_000)
						assert.Nil(t, err)

						return nil
					})
					if err != nil {
						return err
					}

					err = tx.Transaction(func(tx *gorm.DB) error {
						service := invoice_mutations.NewPaymentSubmissionMutation(tx, agent)
						submissionQuery := invoice_query.NewPaymentSubmissionQuery(tx, true)

						submissionQuery.
							JoinInvoice("JOIN").
							WithIDs([]uint{invoice1.ID})

						service, err := service.GetFromQuery(submissionQuery)
						if err != nil {
							return err
						}

						err = service.RejectSubmission()
						assert.Nil(t, err)

						return nil
					})
					if err != nil {
						return err
					}

					t.Run("test data", func(t *testing.T) {
						t.Run("test invoice paid", func(t *testing.T) {
							invoice := db_models.Invoice{}
							invoiceQuery := invoice_query.NewInvoiceQuery(tx, false)
							err := invoiceQuery.WithIDs([]uint{invoice1.ID}).GetQuery().Find(&invoice).Error
							assert.Nil(t, err)
							assert.NotEmpty(t, invoice)
							assert.False(t, invoice.HasSubmission)
							assert.Equal(t, db_models.InvoiceNotPaid, invoice.Status)
						})

						t.Run("tets submission accepted", func(t *testing.T) {
							submission := &db_models.PaymentSubmission{}
							submissionQuery := invoice_query.NewPaymentSubmissionQuery(tx, false)
							err := submissionQuery.JoinInvoice("JOIN").WithIDs([]uint{invoice1.ID}).GetQuery().Find(&submission).Error
							assert.Nil(t, err)
							assert.NotEmpty(t, submission)
							assert.Equal(t, db_models.PaymentSubmissionStatusRejected, submission.Status)

						})
					})

					return errors.New("dummy error")
				})
			})
		},
	)
}
