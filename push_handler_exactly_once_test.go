package invoice_service_test

import (
	"testing"

	"github.com/pdcgo/event_source/event_source_mock"
	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/san_collection/san_config"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestInvoicePushHandlerExactlyOnce covers the message-id inbox: a redelivered message
// (same Pub/Sub MessageID) is skipped so balances aren't double-posted, while a fresh
// MessageID applies again (id-based, not content-based). Mirrors
// inventory_service.TestInventoryPushHandlerExactlyOnce, using the restock COD-fee path.
func TestInvoicePushHandlerExactlyOnce(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "invoice push handler exactly once",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvTransaction{},
					&db_models.RestockCost{},
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalanceDailyLog{},
					&invoice_models.InvoiceExactlyOnceLog{},
				))

				// restock transaction 40: team 1 restocked at warehouse 9, COD fee 25.
				assert.NoError(t, db.Create(&db_models.InvTransaction{ID: 40, TeamID: 1, WarehouseID: 9}).Error)
				assert.NoError(t, db.Create(&db_models.RestockCost{InvTransactionID: 40, CodFee: 25}).Error)

				projectCfg := &san_config.ProjectConfig{ProjectID: "test"}
				handler := invoice_service.NewInvoicePushHandler(db, projectCfg)
				stockSub := projectCfg.PubsubSubscriberPath("invoice-stock-sub")

				restock := &warehouse_iface.StockEvent{
					Data: &warehouse_iface.StockEvent_RestockAccepted{
						RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 40},
					},
				}
				pushID := func(id string) error {
					msg := event_source_mock.NewMockEvent(t, restock)
					msg.Subscription = stockSub
					msg.Message.MessageID = id
					return handler(t.Context(), msg)
				}
				// warehouse 9 is owed the COD fee by team 1 (RECEIVABLE side).
				owedToWarehouse := func() float64 {
					var b invoice_models.TeamBalance
					assert.NoError(t, db.
						Where("team_id = ? AND for_team_id = ? AND balance_type = ?",
							uint64(9), uint64(1), invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE).
						Limit(1).Find(&b).Error)
					return b.Balance
				}
				dedupCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&invoice_models.InvoiceExactlyOnceLog{}).Count(&n).Error)
					return n
				}

				t.Run("first delivery applies the event", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))
					assert.Equal(t, float64(25), owedToWarehouse())
					assert.Equal(t, int64(1), dedupCount())
				})

				t.Run("redelivery with the same message id is a no-op", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))
					assert.Equal(t, float64(25), owedToWarehouse()) // not 50 — the redelivery was deduped
					assert.Equal(t, int64(1), dedupCount())         // still one inbox row
				})

				t.Run("a different message id is processed (id-based, not content-based)", func(t *testing.T) {
					assert.NoError(t, pushID("msg-2"))
					assert.Equal(t, float64(50), owedToWarehouse()) // COD fee applied again under a new id
					assert.Equal(t, int64(2), dedupCount())
				})
			})
		},
	)
}
