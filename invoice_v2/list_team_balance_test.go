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
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const (
	productFee   = invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE
	warehouseFee = invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE
)

func TestListTeamBalance(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "list team balance",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
				))

				now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				// team 1 holds two balances; team 99 is noise that must not leak.
				balances := []*invoice_models.TeamBalance{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, Balance: -50, PendingPaymentAmount: 10, CreatedAt: now, UpdatedAt: now},
					{TeamID: 1, ForTeamID: 3, BalanceType: receivable, Balance: 80, CreatedAt: now, UpdatedAt: now},
					{TeamID: 99, ForTeamID: 2, BalanceType: payable, Balance: -5, CreatedAt: now, UpdatedAt: now},
				}
				assert.NoError(t, tx.Create(&balances).Error)

				t.Run("lists only the scoped team's balances", func(t *testing.T) {
					res, err := svc.ListTeamBalance(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceRequest{
						TeamId: 1,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 2)
				})

				t.Run("filters by balance_type", func(t *testing.T) {
					res, err := svc.ListTeamBalance(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceRequest{
						TeamId:      1,
						BalanceType: receivable,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 1)
					b := res.Msg.Balances[0]
					assert.Equal(t, uint64(3), b.ForTeamId)
					assert.Equal(t, float64(80), b.Balance)
					assert.Equal(t, receivable, b.BalanceType)
				})

				t.Run("filters by for_team_id and maps pending amount", func(t *testing.T) {
					res, err := svc.ListTeamBalance(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceRequest{
						TeamId:    1,
						ForTeamId: 2,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 1)
					b := res.Msg.Balances[0]
					assert.Equal(t, payable, b.BalanceType)
					assert.Equal(t, float64(-50), b.Balance)
					assert.Equal(t, float64(10), b.PendingPaymentAmount)
				})
			})
		},
	)
}

func TestListTeamBalanceLog(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "list team balance log",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.BalanceChangeLog{},
				))

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()
				day := func(d int) time.Time { return time.Date(2026, 6, d, 10, 0, 0, 0, time.UTC) }

				// insertion order fixes the autoincrement id; team 99 is noise.
				logs := []*invoice_models.BalanceChangeLog{
					{TeamID: 1, ForTeamID: 2, ChangeType: productFee, ChangeAmount: 10, BalanceType: receivable, Balance: 10, CreatedByID: 7, CreatedAt: day(1)},
					{TeamID: 1, ForTeamID: 2, ChangeType: productFee, ChangeAmount: 20, BalanceType: receivable, Balance: 30, CreatedByID: 7, CreatedAt: day(5)},
					{TeamID: 1, ForTeamID: 3, ChangeType: warehouseFee, ChangeAmount: 5, BalanceType: payable, Balance: -5, CreatedByID: 7, CreatedAt: day(9)},
					{TeamID: 99, ForTeamID: 2, ChangeType: productFee, ChangeAmount: 1, BalanceType: receivable, Balance: 1, CreatedByID: 7, CreatedAt: day(5)},
				}
				assert.NoError(t, tx.Create(&logs).Error)

				page := func() *common.PageFilter { return &common.PageFilter{Page: 1, Limit: 10} }

				t.Run("page is required", func(t *testing.T) {
					_, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId: 1,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("lists scoped team logs newest first", func(t *testing.T) {
					res, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId: 1,
						Page:   page(),
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 3)
					assert.Equal(t, int64(3), res.Msg.PageInfo.TotalItems)
					// id DESC => the last-inserted team-1 row (for_team 3) comes first.
					assert.Equal(t, uint64(3), res.Msg.Logs[0].ForTeamId)
				})

				t.Run("filters by created_at time window", func(t *testing.T) {
					res, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId:   1,
						FromTime: timestamppb.New(day(4)),
						ToTime:   timestamppb.New(day(6)),
						Page:     page(),
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 1)
					assert.Equal(t, float64(20), res.Msg.Logs[0].ChangeAmount)
				})

				t.Run("filters by balance_type", func(t *testing.T) {
					res, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId:      1,
						BalanceType: payable,
						Page:        page(),
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 1)
					assert.Equal(t, warehouseFee, res.Msg.Logs[0].ChangeType)
				})

				t.Run("paginates", func(t *testing.T) {
					res, err := svc.ListTeamBalanceLog(ctx, connect.NewRequest(&invoice_iface.ListTeamBalanceLogRequest{
						TeamId: 1,
						Page:   &common.PageFilter{Page: 1, Limit: 2},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 2)
					assert.Equal(t, int64(3), res.Msg.PageInfo.TotalItems)
					assert.Equal(t, int64(2), res.Msg.PageInfo.TotalPage)
				})
			})
		},
	)
}

func TestTeamBalanceMap(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "team balance map",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.TeamBalance{},
				))

				now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				// team 1's balances against three counterparties; team 99 is noise.
				balances := []*invoice_models.TeamBalance{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, Balance: -50, CreatedAt: now, UpdatedAt: now},
					{TeamID: 1, ForTeamID: 3, BalanceType: receivable, Balance: 80, CreatedAt: now, UpdatedAt: now},
					{TeamID: 1, ForTeamID: 4, BalanceType: payable, Balance: -10, CreatedAt: now, UpdatedAt: now},
					{TeamID: 99, ForTeamID: 2, BalanceType: payable, Balance: -5, CreatedAt: now, UpdatedAt: now},
				}
				assert.NoError(t, tx.Create(&balances).Error)

				t.Run("maps all counterparties keyed by for_team_id", func(t *testing.T) {
					res, err := svc.TeamBalanceMap(ctx, connect.NewRequest(&invoice_iface.TeamBalanceMapRequest{
						TeamId: 1,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 3)
					_, ok2 := res.Msg.Balances[2]
					_, ok3 := res.Msg.Balances[3]
					_, ok4 := res.Msg.Balances[4]
					assert.True(t, ok2 && ok3 && ok4)
					// counterparty 3 mapped correctly
					assert.Equal(t, receivable, res.Msg.Balances[3].BalanceType)
					assert.Equal(t, float64(80), res.Msg.Balances[3].Balance)
				})

				t.Run("filters by for_team_ids", func(t *testing.T) {
					res, err := svc.TeamBalanceMap(ctx, connect.NewRequest(&invoice_iface.TeamBalanceMapRequest{
						TeamId:     1,
						ForTeamIds: []uint64{2, 4},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 2)
					_, ok3 := res.Msg.Balances[3]
					assert.False(t, ok3)
					assert.Equal(t, float64(-50), res.Msg.Balances[2].Balance)
				})

				t.Run("filters by balance_type", func(t *testing.T) {
					res, err := svc.TeamBalanceMap(ctx, connect.NewRequest(&invoice_iface.TeamBalanceMapRequest{
						TeamId:      1,
						BalanceType: payable,
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Balances, 2)
					_, ok2 := res.Msg.Balances[2]
					_, ok4 := res.Msg.Balances[4]
					assert.True(t, ok2 && ok4)
				})
			})
		},
	)
}
