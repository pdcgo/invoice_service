package invoice_v2_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestListTeamBalanceLogOrderScope(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "list team balance log order scope",
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
				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				// V3 order 5 product fee: owner 8 <- ordering team 1.
				assert.NoError(t, invoice_v2.PostBalanceLog(tx, 8, 1,
					invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE,
					13, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					"v3 order 5 product fee", callerID, now,
					&invoice_v2.OrderSource{OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_V3, OrderID: 5, TeamID: 1, WarehouseID: 9}))

				// LEGACY order 5 (colliding id) warehouse fee: warehouse 9 <- ordering team 1.
				assert.NoError(t, invoice_v2.PostBalanceLog(tx, 9, 1,
					invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE,
					1000, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					"legacy order 5 warehouse fee", callerID, now,
					&invoice_v2.OrderSource{OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_LEGACY, OrderID: 5, TeamID: 1, WarehouseID: 9}))

				// Non-order adjustment: creditor 2 <- team 1, no source.
				assert.NoError(t, invoice_v2.PostBalanceLog(tx, 2, 1,
					invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
					50, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
					"adjustment", callerID, now))

				list := func(req *invoice_iface.ListTeamBalanceLogRequest) []*invoice_iface.BalanceChangeLog {
					req.Page = &common.PageFilter{Page: 1, Limit: 100}
					res, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(req))
					assert.NoError(t, err)
					return res.Msg.Logs
				}

				t.Run("order_id + order_system=V3 returns only the V3 leg", func(t *testing.T) {
					logs := list(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId:      1,
						OrderId:     5,
						OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_V3,
					})
					assert.Len(t, logs, 1)
					assert.Equal(t, uint64(5), logs[0].OrderId)
					assert.Equal(t, uint64(9), logs[0].WarehouseId)
					assert.Equal(t, invoice_iface.OrderSystem_ORDER_SYSTEM_V3, logs[0].OrderSystem)
					assert.Equal(t, invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE, logs[0].ChangeType)
				})

				t.Run("colliding order_id disambiguated by order_system=LEGACY", func(t *testing.T) {
					logs := list(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId:      1,
						OrderId:     5,
						OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_LEGACY,
					})
					assert.Len(t, logs, 1)
					assert.Equal(t, invoice_iface.OrderSystem_ORDER_SYSTEM_LEGACY, logs[0].OrderSystem)
					assert.Equal(t, invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE, logs[0].ChangeType)
				})

				t.Run("warehouse_id filters to order-sourced legs only", func(t *testing.T) {
					logs := list(&invoice_iface.ListTeamBalanceLogRequest{TeamId: 1, WarehouseId: 9})
					assert.Len(t, logs, 2) // V3 + LEGACY; the adjustment has no source
				})

				t.Run("no filter returns all team legs, source surfaced where present", func(t *testing.T) {
					logs := list(&invoice_iface.ListTeamBalanceLogRequest{TeamId: 1})
					assert.Len(t, logs, 3)
					var adj *invoice_iface.BalanceChangeLog
					for _, l := range logs {
						if l.ChangeType == invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT {
							adj = l
						}
					}
					assert.NotNil(t, adj)
					assert.Equal(t, uint64(0), adj.OrderId) // non-order row carries 0
				})
			})
		},
	)
}
