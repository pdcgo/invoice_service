package invoice_service_test

import (
	"testing"
	"time"

	"github.com/pdcgo/event_source/event_source_mock"
	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/schema/services/selling_iface/v1"
	"github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const (
	receivable   = invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
	payable      = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
	productFee   = invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE
	warehouseFee = invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE
	codFee       = invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_COD_FEE
)

func TestInvoicePushHandler(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "invoice push handler",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.Order{},
					&db_models.OrderItem{},
					&db_models.Product{},
					&db_models.InvTransaction{},
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalanceDailyLog{},
				))

				// order team 1 sells products owned by team 2 (cross items), plus one owned item.
				products := []*db_models.Product{
					{ID: 1, TeamID: 2},
					{ID: 2, TeamID: 2},
					{ID: 3, TeamID: 1},
				}
				assert.NoError(t, db.Create(&products).Error)

				// inv transaction supplies the warehouse team (warehouse_id 9) that
				// charges the order's warehouse fee.
				invTx := &db_models.InvTransaction{ID: 10, TeamID: 1, WarehouseID: 9}
				assert.NoError(t, db.Create(invTx).Error)
				invTxID := uint(10)

				order := &db_models.Order{
					ID:            1,
					TeamID:        1,
					OrderRefID:    "ORD-1",
					CreatedByID:   7,
					InvertoryTxID: &invTxID,
					WarehouseFee:  15,
					Items: []*db_models.OrderItem{
						{OrderID: 1, ProductID: 1, Owned: false, Total: 30, Count: 1, ProductName: "A"},
						{OrderID: 1, ProductID: 2, Owned: false, Total: 20, Count: 1, ProductName: "B"},
						{OrderID: 1, ProductID: 3, Owned: true, Total: 99, Count: 1, ProductName: "C"},
					},
				}
				assert.NoError(t, db.Create(order).Error)

				projectCfg := &invoice_service.ProjectConfig{ProjectID: "test"}
				handler := invoice_service.NewInvoicePushHandler(db, projectCfg)
				txTime := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)

				balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalance, bool) {
					var b invoice_models.TeamBalance
					res := db.Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).Limit(1).Find(&b)
					assert.NoError(t, res.Error)
					return b, res.RowsAffected > 0
				}
				dailyOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalanceDailyLog, bool) {
					var d invoice_models.TeamBalanceDailyLog
					res := db.Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).Limit(1).Find(&d)
					assert.NoError(t, res.Error)
					return d, res.RowsAffected > 0
				}
				logCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&invoice_models.BalanceChangeLog{}).Count(&n).Error)
					return n
				}

				t.Run("order created aggregates into balance + daily log", func(t *testing.T) {
					msg := event_source_mock.NewMockEvent(t, &selling_iface.SellingEvent{
						Data: &selling_iface.SellingEvent_OrderCreated{
							OrderCreated: &selling_iface.OrderCreated{
								OrderId:         1,
								TransactionTime: timestamppb.New(txTime),
							},
						},
					})
					msg.Subscription = projectCfg.PubsubSubscriberPath("invoice-selling-sub")
					assert.NoError(t, handler(t.Context(), msg))

					// owned item (99) excluded; cross items 30+20 = 50.
					rcv, ok := balanceOf(2, 1, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(50), rcv.Balance)
					pyb, ok := balanceOf(1, 2, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(-50), pyb.Balance)

					rcvDaily, ok := dailyOf(2, 1, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(0), rcvDaily.StartBalance)
					assert.Equal(t, float64(50), rcvDaily.EndBalance)
					assert.Equal(t, float64(50), rcvDaily.ChangeAmount)
					pybDaily, ok := dailyOf(1, 2, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(-50), pybDaily.EndBalance)
					assert.Equal(t, float64(-50), pybDaily.ChangeAmount)

					// warehouse fee: order team 1 owes warehouse team 9 the fee (15).
					whRcv, ok := balanceOf(9, 1, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(15), whRcv.Balance)
					whPyb, ok := balanceOf(1, 9, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(-15), whPyb.Balance)

					whRcvDaily, ok := dailyOf(9, 1, receivable)
					assert.True(t, ok)
					assert.Equal(t, float64(15), whRcvDaily.ChangeAmount)
					assert.Equal(t, float64(15), whRcvDaily.EndBalance)
					whPybDaily, ok := dailyOf(1, 9, payable)
					assert.True(t, ok)
					assert.Equal(t, float64(-15), whPybDaily.ChangeAmount)

					var whLog invoice_models.BalanceChangeLog
					assert.NoError(t, db.Where("team_id = ? AND for_team_id = ?", uint64(9), uint64(1)).First(&whLog).Error)
					assert.Equal(t, warehouseFee, whLog.ChangeType)
					assert.Equal(t, uint64(7), whLog.CreatedByID)

					// 2 cross items x 2 legs + 1 warehouse fee x 2 legs.
					assert.Equal(t, int64(6), logCount())
					var anyLog invoice_models.BalanceChangeLog
					assert.NoError(t, db.Where("team_id = ? AND for_team_id = ?", uint64(2), uint64(1)).First(&anyLog).Error)
					assert.Equal(t, productFee, anyLog.ChangeType)
					assert.Equal(t, uint64(7), anyLog.CreatedByID)
				})

				t.Run("order canceled reverses to zero", func(t *testing.T) {
					msg := event_source_mock.NewMockEvent(t, &selling_iface.SellingEvent{
						Data: &selling_iface.SellingEvent_OrderCanceled{
							OrderCanceled: &selling_iface.OrderCanceled{
								OrderId:         1,
								TransactionTime: timestamppb.New(txTime),
							},
						},
					})
					msg.Subscription = projectCfg.PubsubSubscriberPath("invoice-selling-sub")
					assert.NoError(t, handler(t.Context(), msg))

					rcv, _ := balanceOf(2, 1, receivable)
					assert.Equal(t, float64(0), rcv.Balance)
					pyb, _ := balanceOf(1, 2, payable)
					assert.Equal(t, float64(0), pyb.Balance)

					// same-day daily rows net out.
					rcvDaily, _ := dailyOf(2, 1, receivable)
					assert.Equal(t, float64(0), rcvDaily.ChangeAmount)
					assert.Equal(t, float64(0), rcvDaily.EndBalance)
					pybDaily, _ := dailyOf(1, 2, payable)
					assert.Equal(t, float64(0), pybDaily.ChangeAmount)
					assert.Equal(t, float64(0), pybDaily.EndBalance)

					// warehouse fee reverses to zero too.
					whRcv, _ := balanceOf(9, 1, receivable)
					assert.Equal(t, float64(0), whRcv.Balance)
					whPyb, _ := balanceOf(1, 9, payable)
					assert.Equal(t, float64(0), whPyb.Balance)

					// 6 (create) + 6 (cancel) legs.
					assert.Equal(t, int64(12), logCount())
				})
			})
		},
	)
}

func TestInvoicePushHandlerRestockCodFee(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "invoice push handler restock cod fee",
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
				))

				// restock transaction 20: team 1 restocked at warehouse 9, COD fee 25.
				assert.NoError(t, db.Create(&db_models.InvTransaction{ID: 20, TeamID: 1, WarehouseID: 9}).Error)
				assert.NoError(t, db.Create(&db_models.RestockCost{InvTransactionID: 20, CodFee: 25}).Error)

				projectCfg := &invoice_service.ProjectConfig{ProjectID: "test"}
				handler := invoice_service.NewInvoicePushHandler(db, projectCfg)

				balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) (invoice_models.TeamBalance, bool) {
					var b invoice_models.TeamBalance
					res := db.Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).Limit(1).Find(&b)
					assert.NoError(t, res.Error)
					return b, res.RowsAffected > 0
				}

				msg := event_source_mock.NewMockEvent(t, &warehouse_iface.StockEvent{
					Data: &warehouse_iface.StockEvent_RestockAccepted{
						RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 20},
					},
				})
				msg.Subscription = projectCfg.PubsubSubscriberPath("invoice-stock-sub")
				assert.NoError(t, handler(t.Context(), msg))

				// team 1 owes warehouse 9 the COD fee (25): warehouse receivable +25,
				// team payable -25.
				rcv, ok := balanceOf(9, 1, receivable)
				assert.True(t, ok)
				assert.Equal(t, float64(25), rcv.Balance)
				pyb, ok := balanceOf(1, 9, payable)
				assert.True(t, ok)
				assert.Equal(t, float64(-25), pyb.Balance)

				// one fee x 2 legs.
				var n int64
				assert.NoError(t, db.Model(&invoice_models.BalanceChangeLog{}).Count(&n).Error)
				assert.Equal(t, int64(2), n)

				var codLog invoice_models.BalanceChangeLog
				assert.NoError(t, db.Where("team_id = ? AND for_team_id = ?", uint64(9), uint64(1)).First(&codLog).Error)
				assert.Equal(t, codFee, codLog.ChangeType)

				t.Run("transaction without cod fee posts nothing", func(t *testing.T) {
					assert.NoError(t, db.Create(&db_models.InvTransaction{ID: 21, TeamID: 1, WarehouseID: 9}).Error)
					msg := event_source_mock.NewMockEvent(t, &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 21},
						},
					})
					msg.Subscription = projectCfg.PubsubSubscriberPath("invoice-stock-sub")
					assert.NoError(t, handler(t.Context(), msg))

					// balances and log count unchanged (no restock_cost row → cod_fee 0).
					rcv, _ := balanceOf(9, 1, receivable)
					assert.Equal(t, float64(25), rcv.Balance)
					var n int64
					assert.NoError(t, db.Model(&invoice_models.BalanceChangeLog{}).Count(&n).Error)
					assert.Equal(t, int64(2), n)
				})
			})
		},
	)
}
