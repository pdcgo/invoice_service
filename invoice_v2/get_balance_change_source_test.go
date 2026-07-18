package invoice_v2_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func SeedBalanceChangeSources(scenario *moretest_mock.DbScenario) moretest.SetupFunc {
	return func(t *testing.T) func() error {
		inner := moretest_mock.MockPostgresDatabase(scenario)
		teardown := inner(t)

		withDb := *scenario
		*scenario = func(t *testing.T, handler func(tx *gorm.DB)) {
			withDb(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
					&invoice_models.BalanceChangeOrderSource{},
					&invoice_models.BalanceChangeRestockSource{},
					&invoice_models.BalanceChangeBrokenSource{},
				))

				legacy := invoice_iface.OrderSystem_ORDER_SYSTEM_LEGACY
				v3 := invoice_iface.OrderSystem_ORDER_SYSTEM_V3

				// The logs the sources attribute to. team_id is the scoping key: every id
				// below belongs to team 3 EXCEPT log 3 (team 4), which exists to prove a
				// caller scoped to team 3 cannot read another team's attribution. Log 30 is
				// a team-3 change with no source row (adjustment) — owned but unattributed.
				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeLog{
					{ID: 1, TeamID: 3, ForTeamID: 5}, {ID: 2, TeamID: 3, ForTeamID: 5},
					{ID: 3, TeamID: 4, ForTeamID: 5}, {ID: 4, TeamID: 3, ForTeamID: 5},
					{ID: 10, TeamID: 3, ForTeamID: 5}, {ID: 11, TeamID: 3, ForTeamID: 5},
					{ID: 12, TeamID: 3, ForTeamID: 5}, {ID: 20, TeamID: 3, ForTeamID: 5},
					{ID: 30, TeamID: 3, ForTeamID: 5},
				}).Error)

				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeOrderSource{
					{BalanceChangeLogID: 1, OrderSystem: legacy, OrderID: 500, OrderItemID: 77, TeamID: 3, WarehouseID: 9},
					{BalanceChangeLogID: 2, OrderSystem: legacy, OrderID: 500, TeamID: 3, WarehouseID: 9},
					{BalanceChangeLogID: 3, OrderSystem: v3, OrderID: 500, TeamID: 4, WarehouseID: 9},
					{BalanceChangeLogID: 4, OrderSystem: legacy, OrderID: 999, TeamID: 3, WarehouseID: 9},
				}).Error)

				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeRestockSource{
					{BalanceChangeLogID: 10, TxID: 900, TeamID: 3, WarehouseID: 9},
					{BalanceChangeLogID: 11, TxID: 900, TeamID: 3, WarehouseID: 9},
					{BalanceChangeLogID: 12, TxID: 901, TeamID: 3, WarehouseID: 9},
				}).Error)

				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeBrokenSource{
					{BalanceChangeLogID: 20, TxID: 900, TxItemID: 55, TeamID: 3, WarehouseID: 9},
				}).Error)

				handler(tx)
			})
		}

		return teardown
	}
}

func TestGetBalanceChangeSource(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "get balance change source",
		moretest.SetupListFunc{
			SeedBalanceChangeSources(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				// Every request is scoped to team 3 (the caller); the interceptor would
				// enforce membership, and the handler filters by it.
				get := func(req *invoice_iface.GetBalanceChangeSourceRequest) *invoice_iface.GetBalanceChangeSourceResponse {
					req.TeamId = 3
					res, err := svc.GetBalanceChangeSource(ctx, connect.NewRequest(req))
					assert.NoError(t, err)
					return res.Msg
				}

				t.Run("order returns every leg, item id 0 when order-level", func(t *testing.T) {
					msg := get(&invoice_iface.GetBalanceChangeSourceRequest{
						SourceType: &invoice_iface.GetBalanceChangeSourceRequest_Order{
							Order: &invoice_iface.BalanceChangeOrderSourceType{
								OrderId:     500,
								OrderSystem: invoice_iface.OrderSystem_ORDER_SYSTEM_LEGACY,
							},
						},
					})
					items := msg.GetOrder().GetItems()
					assert.Len(t, items, 2) // the v3 order 500 and the legacy order 999 are excluded

					assert.Equal(t, uint64(1), items[0].GetBalanceChangeLogId())
					assert.Equal(t, uint64(500), items[0].GetOrderId())
					assert.Equal(t, uint64(77), items[0].GetOrderItemId())
					assert.Equal(t, uint64(3), items[0].GetTeamId())
					assert.Equal(t, uint64(9), items[0].GetWarehouseId())
					assert.NotNil(t, items[0].GetCreatedAt())

					// The order-level leg: NULL order_item_id surfaces as 0.
					assert.Equal(t, uint64(2), items[1].GetBalanceChangeLogId())
					assert.Equal(t, uint64(0), items[1].GetOrderItemId())
				})

				t.Run("unspecified order_system spans both id-spaces, but scope excludes other teams", func(t *testing.T) {
					msg := get(&invoice_iface.GetBalanceChangeSourceRequest{
						SourceType: &invoice_iface.GetBalanceChangeSourceRequest_Order{
							Order: &invoice_iface.BalanceChangeOrderSourceType{OrderId: 500},
						},
					})
					// Order 500 has 3 legs sharing the id, but log 3 belongs to team 4, so a
					// team-3 caller sees only the two team-3 (legacy) legs.
					assert.Len(t, msg.GetOrder().GetItems(), 2)
				})

				t.Run("restock is keyed by tx, cod fee has no item", func(t *testing.T) {
					msg := get(&invoice_iface.GetBalanceChangeSourceRequest{
						SourceType: &invoice_iface.GetBalanceChangeSourceRequest_Restock{
							Restock: &invoice_iface.BalanceChangeRestockSourceType{TxId: 900},
						},
					})
					items := msg.GetRestock().GetItems()
					assert.Len(t, items, 2) // tx 901 excluded
					assert.Equal(t, uint64(10), items[0].GetBalanceChangeLogId())
					assert.Equal(t, uint64(900), items[0].GetTxId())
					assert.Equal(t, uint64(0), items[0].GetTxItemId()) // cod is transaction-level
					assert.Nil(t, msg.GetBroken())                     // the arm mirrors the request
				})

				t.Run("broken is independent of restock on the same tx", func(t *testing.T) {
					msg := get(&invoice_iface.GetBalanceChangeSourceRequest{
						SourceType: &invoice_iface.GetBalanceChangeSourceRequest_Broken{
							Broken: &invoice_iface.BalanceChangeBrokenSourceType{TxId: 900},
						},
					})
					items := msg.GetBroken().GetItems()
					assert.Len(t, items, 1) // the restock rows on tx 900 are a different table
					assert.Equal(t, uint64(20), items[0].GetBalanceChangeLogId())
					assert.Equal(t, uint64(55), items[0].GetTxItemId())
					assert.Nil(t, msg.GetRestock())
				})

				t.Run("unknown source reports an empty list, not an error", func(t *testing.T) {
					msg := get(&invoice_iface.GetBalanceChangeSourceRequest{
						SourceType: &invoice_iface.GetBalanceChangeSourceRequest_Restock{
							Restock: &invoice_iface.BalanceChangeRestockSourceType{TxId: 404},
						},
					})
					assert.NotNil(t, msg.GetRestock()) // the arm is still set
					assert.Empty(t, msg.GetRestock().GetItems())
				})

				t.Run("missing source_type is invalid", func(t *testing.T) {
					_, err := svc.GetBalanceChangeSource(ctx, connect.NewRequest(&invoice_iface.GetBalanceChangeSourceRequest{}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("by change ids resolves each id to its source, scoped to the team", func(t *testing.T) {
					// Mix an order leg (1), a restock leg (10), a broken leg (20), a change
					// with no source row (30), a leg belonging to ANOTHER team (3), and an id
					// that does not exist (777). Only ids that resolve to a source appear.
					res, err := svc.GetBalanceChangeSourceByChangeIds(ctx, connect.NewRequest(&invoice_iface.GetBalanceChangeSourceByChangeIdsRequest{
						TeamId:              3,
						BalanceChangeLogIds: []uint64{1, 10, 20, 30, 3, 777},
					}))
					assert.NoError(t, err)
					entries := res.Msg.GetEntries()

					assert.Len(t, entries, 3)
					assert.Contains(t, entries, uint64(1))
					assert.Contains(t, entries, uint64(10))
					assert.Contains(t, entries, uint64(20))
					assert.NotContains(t, entries, uint64(30))  // no source row (adjustment)
					assert.NotContains(t, entries, uint64(3))   // belongs to team 4
					assert.NotContains(t, entries, uint64(777)) // does not exist

					// Each present id carries the right arm.
					assert.Equal(t, uint64(500), entries[1].GetOrder().GetOrderId())
					assert.Equal(t, uint64(77), entries[1].GetOrder().GetOrderItemId())
					assert.Equal(t, uint64(900), entries[10].GetRestock().GetTxId())
					assert.Equal(t, uint64(55), entries[20].GetBroken().GetTxItemId())
				})

				t.Run("by change ids: another team's leg is never resolved", func(t *testing.T) {
					// Log 3 has an order source, but it belongs to team 4; a team-3 caller
					// must not see it.
					res, err := svc.GetBalanceChangeSourceByChangeIds(ctx, connect.NewRequest(&invoice_iface.GetBalanceChangeSourceByChangeIdsRequest{
						TeamId:              3,
						BalanceChangeLogIds: []uint64{3, 777},
					}))
					assert.NoError(t, err)
					assert.Empty(t, res.Msg.GetEntries())
				})
			})
		},
	)
}
