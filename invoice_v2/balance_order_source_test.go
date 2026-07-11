package invoice_v2_test

import (
	"testing"
	"time"

	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestPostBalanceLogOrderSource(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "post balance log order source",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalance{},
					&invoice_models.TeamBalanceDailyLog{},
					&invoice_models.BalanceChangeOrderSource{},
				))
				now := time.Now()

				count := func(model interface{}) int64 {
					var n int64
					assert.NoError(t, tx.Model(model).Count(&n).Error)
					return n
				}

				t.Run("order-sourced post writes a source row per leg", func(t *testing.T) {
					err := invoice_v2.PostBalanceLog(tx, 8, 1,
						invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE,
						13, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
						"order 555 product fee", callerID, now,
						&invoice_v2.OrderSource{
							OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_V3,
							OrderID:     555,
							TeamID:      1,
							WarehouseID: 9,
						})
					assert.NoError(t, err)

					// double entry = 2 ledger legs, and one source row per leg.
					assert.Equal(t, int64(2), count(&invoice_models.BalanceChangeLog{}))
					assert.Equal(t, int64(2), count(&invoice_models.BalanceChangeOrderSource{}))

					// each source row references a real log leg and carries the order attribution.
					var sources []invoice_models.BalanceChangeOrderSource
					assert.NoError(t, tx.Find(&sources).Error)
					for _, s := range sources {
						assert.Equal(t, uint64(555), s.OrderID)
						assert.Equal(t, uint64(1), s.TeamID)
						assert.Equal(t, uint64(9), s.WarehouseID)
						assert.Equal(t, invoice_iface.OrderSystem_ORDER_SYSTEM_V3, s.OrderSystem)

						var log invoice_models.BalanceChangeLog
						assert.NoError(t, tx.Where("id = ?", s.BalanceChangeLogID).First(&log).Error)
					}
				})

				t.Run("post without source writes no source rows", func(t *testing.T) {
					err := invoice_v2.PostBalanceLog(tx, 8, 1,
						invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
						5, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
						"no source", callerID, now)
					assert.NoError(t, err)

					// still only the 2 source rows from the previous subtest.
					assert.Equal(t, int64(2), count(&invoice_models.BalanceChangeOrderSource{}))
				})
			})
		},
	)
}
