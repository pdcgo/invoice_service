package invoice_mutations_test

import (
	"testing"
	"time"

	"github.com/pdcgo/invoice_service/invoice_mutations"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/identity/mock_identity"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestInvoiceManage(t *testing.T) {
	var db gorm.DB
	var migrate moretest.SetupFunc = func(t *testing.T) func() error {
		err := db.AutoMigrate(
			&db_models.Invoice{},
		)

		assert.Nil(t, err)
		return nil
	}

	agent := mock_identity.NewMockAgent(1, "test")

	moretest.Suite(t, "testing invoice",
		moretest.SetupListFunc{
			moretest_mock.MockSqliteDatabase(&db),
			migrate,
		},
		func(t *testing.T) {
			moretest.Suite(t, "test invoice paid",
				moretest.SetupListFunc{
					func(t *testing.T) func() error {
						var txID uint = 1
						invos := []*db_models.Invoice{
							{
								ID:         1,
								TxID:       &txID,
								FromTeamID: 1,
								ToTeamID:   2,
								Type:       db_models.InvoShipFeeType,
								Status:     db_models.InvoicePaid,
								Amount:     2000,
								Created:    time.Now(),
							},
						}

						err := db.Save(&invos).Error
						assert.Nil(t, err)

						return func() error {
							return db.Model(&db_models.Invoice{}).Where("from_team_id = ?", 1).Delete(&db_models.Invoice{}).Error
						}
					},
				},
				func(t *testing.T) {
					var txID uint = 1
					err := db.Transaction(func(tx *gorm.DB) error {
						return invoice_mutations.
							NewInvoiceManage(tx, agent).
							Get(invoice_mutations.NewInvoiceQuery(tx, true)).
							ReadjustAmount(&invoice_mutations.ReadjustAmountPayload{
								FromID: 1,
								ToID:   2,
								TxID:   &txID,
								Amount: 4000,
							}).
							Err()
					})
					assert.Nil(t, err)

					t.Run("testing isi invoice", func(t *testing.T) {
						invos := []*db_models.Invoice{}
						err = db.Model(&db_models.Invoice{}).Order("id asc").Find(&invos).Error
						assert.Nil(t, err)
						assert.Len(t, invos, 2)

						notpaid := invos[1]
						assert.Equal(t, db_models.InvoiceNotPaid, notpaid.Status)
						assert.Equal(t, db_models.InvoShipFeeType, notpaid.Type)

						for _, inv := range invos {
							assert.Equal(t, 2000.00, inv.Amount)
						}

					})
				},
			)
			moretest.Suite(t, "test invoice unpaid",
				moretest.SetupListFunc{
					func(t *testing.T) func() error {
						var txID uint = 1
						invos := []*db_models.Invoice{
							{
								ID:         1,
								TxID:       &txID,
								FromTeamID: 1,
								ToTeamID:   2,
								Type:       db_models.InvoShipFeeType,
								Status:     db_models.InvoiceNotPaid,
								Amount:     3000,
								Created:    time.Now(),
							},
						}

						err := db.Save(&invos).Error
						assert.Nil(t, err)

						return func() error {
							return db.Model(&db_models.Invoice{}).Where("from_team_id = ?", 1).Delete(&db_models.Invoice{}).Error
						}
					},
				},
				func(t *testing.T) {
					var txID uint = 1
					err := db.Transaction(func(tx *gorm.DB) error {
						return invoice_mutations.
							NewInvoiceManage(tx, agent).
							Get(invoice_mutations.NewInvoiceQuery(tx, true)).
							ReadjustAmount(&invoice_mutations.ReadjustAmountPayload{
								FromID: 1,
								ToID:   2,
								TxID:   &txID,
								Amount: 6000,
							}).
							Err()
					})
					assert.Nil(t, err)

					t.Run("testing isi invoice", func(t *testing.T) {
						invos := []*db_models.Invoice{}
						err = db.Model(&db_models.Invoice{}).Order("id asc").Find(&invos).Error

						// debugtool.LogJson(invos)

						assert.Nil(t, err)
						assert.Len(t, invos, 2)

						notpaid := invos[1]
						assert.Equal(t, db_models.InvoiceNotPaid, notpaid.Status)
						assert.Equal(t, db_models.InvoShipFeeType, notpaid.Type)
						assert.Equal(t, 6000.00, notpaid.Amount)
					})
				},
			)
		},
	)
}
