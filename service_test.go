package invoice_service_test

import (
	"testing"

	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/interfaces/invoice_iface"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/pdcgo/shared/pkg/ware_cache"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestLimitInvoice(t *testing.T) {
	var db gorm.DB

	moretest.Suite(t, "testing limit hutang invoice",
		moretest.SetupListFunc{
			moretest_mock.MockSqliteDatabase(&db),
			func(t *testing.T) func() error { // migrating
				err := db.AutoMigrate(
					&invoice_service.InvoiceLimitConfiguration{},
					&db_models.Team{},
					&db_models.Invoice{},
				)
				assert.Nil(t, err)
				return nil
			},
			func(t *testing.T) func() error { // seeding
				teams := []*db_models.Team{
					{
						ID:       1,
						Type:     db_models.SellingTeamType,
						Name:     "team satu",
						TeamCode: "T1",
					},
					{
						ID:       2,
						Type:     db_models.SellingTeamType,
						Name:     "team dua",
						TeamCode: "T2",
					},
					{
						ID:       3,
						Type:     db_models.SellingTeamType,
						Name:     "team tiga",
						TeamCode: "T3",
					},
				}

				err := db.Save(&teams).Error
				assert.Nil(t, err)

				invoices := []*db_models.Invoice{
					{
						Amount:     100,
						Status:     db_models.InvoiceNotPaid,
						FromTeamID: 2,
						ToTeamID:   1,
					},
				}

				err = db.Save(&invoices).Error
				assert.Nil(t, err)

				return nil
			},
		},
		func(t *testing.T) {

			service := invoice_service.NewInvoiceService(&db, ware_cache.NewLocalCache())

			t.Run("test create configuration default", func(t *testing.T) {
				_, err := service.SetLimitInvoice(t.Context(), &invoice_iface.SetLimitInvoiceReq{
					TeamId:    1,
					Threshold: 2000,
				})

				assert.Nil(t, err)

				t.Run("validasi data", func(t *testing.T) {
					t.Run("dengan team_id 0", func(t *testing.T) {
						_, err := service.SetLimitInvoice(t.Context(), &invoice_iface.SetLimitInvoiceReq{
							TeamId:    0,
							Threshold: 2000,
						})
						assert.NotNil(t, err)
					})

					t.Run("dengan team_id tidak ada", func(t *testing.T) {
						_, err := service.SetLimitInvoice(t.Context(), &invoice_iface.SetLimitInvoiceReq{
							TeamId:    99,
							Threshold: 200,
						})
						assert.NotNil(t, err)
					})

				})

				t.Run("check data", func(t *testing.T) {
					confs := []*invoice_service.InvoiceLimitConfiguration{}
					err := db.Model(&invoice_service.InvoiceLimitConfiguration{}).Find(&confs).Error
					assert.Nil(t, err)

					assert.Len(t, confs, 1)
					for _, conf := range confs {
						assert.Equal(t, invoice_iface.LimitType_DEFAULT, conf.LimitType)
						assert.Equal(t, int64(1), conf.TeamID)
						assert.Equal(t, float64(2000), conf.Threshold)
					}

				})

			})

			t.Run("test dengan for team", func(t *testing.T) {
				t.Run("dengan for_team_id tidak ada", func(t *testing.T) {
					var forteamID int64 = 60
					_, err := service.SetLimitInvoice(t.Context(), &invoice_iface.SetLimitInvoiceReq{
						TeamId:    1,
						Threshold: 2005,
						ForTeamId: &forteamID,
					})
					assert.NotNil(t, err)
				})

				var forTeamID int64 = 2
				_, err := service.SetLimitInvoice(t.Context(), &invoice_iface.SetLimitInvoiceReq{
					TeamId:    1,
					Threshold: 3000,
					ForTeamId: &forTeamID,
				})

				assert.Nil(t, err)

				t.Run("test data", func(t *testing.T) {
					confs := []*invoice_service.InvoiceLimitConfiguration{}
					err := db.
						Model(&invoice_service.InvoiceLimitConfiguration{}).
						Where("limit_type = ?", invoice_iface.LimitType_TEAM).
						Find(&confs).Error
					assert.Nil(t, err)

					assert.Len(t, confs, 1)
					for _, conf := range confs {
						assert.Equal(t, invoice_iface.LimitType_TEAM, conf.LimitType)
						assert.Equal(t, int64(1), conf.TeamID)
						assert.Equal(t, int64(2), *conf.ForTeamID)
						assert.Equal(t, float64(3000), conf.Threshold)
					}
				})

			})

			t.Run("test list data konfigurasi", func(t *testing.T) {

				res, err := service.LimitInvoiceList(t.Context(), &invoice_iface.ConfigListReq{
					TeamId: 1,
				})

				assert.Nil(t, err)
				for _, d := range res.Data {
					assert.NotEmpty(t, d.Team)
					if d.ForTeamId != nil {
						assert.NotEmpty(t, d.ForTeam)
					}
				}

			})

			t.Run("test get limit invoice", func(t *testing.T) {
				t.Run("testing dengan team_id kosong", func(t *testing.T) {
					_, err := service.GetLimitInvoice(t.Context(), &invoice_iface.TeamLimitInvoiceReq{
						TeamId: 0,
					})
					assert.NotNil(t, err)
				})

				t.Run("testing normal", func(t *testing.T) {
					_, err := service.GetLimitInvoice(t.Context(), &invoice_iface.TeamLimitInvoiceReq{
						TeamId:    1,
						ForTeamId: 2,
					})
					assert.Nil(t, err)
				})

			})

			t.Run("test delete config", func(t *testing.T) {
				t.Run("with team id kosong", func(t *testing.T) {
					_, err := service.LimitInvoiceDelete(t.Context(), &invoice_iface.LimitInvoiceDeleteReq{
						TeamId: 0,
					})
					assert.NotNil(t, err)
				})

				t.Run("running normal", func(t *testing.T) {
					var forTeamID int64 = 2
					_, err := service.LimitInvoiceDelete(t.Context(), &invoice_iface.LimitInvoiceDeleteReq{
						TeamId:    1,
						ForTeamId: &forTeamID,
					})
					assert.Nil(t, err)
				})
			})
		},
	)

}
