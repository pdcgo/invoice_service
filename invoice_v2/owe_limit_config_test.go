package invoice_v2_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestOweLimitConfigCRUD(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "owe limit config crud",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&db_models.OweLimitConfiguration{},
					&invoice_models.TeamBalance{},
					&teamRow{},
				))

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				const creditor = uint64(8) // the limit owner
				const debtor = uint64(1)

				// OweLimitCustomList now lists every selling counterparty (except the
				// creditor). Seed the debtor as a selling team so it appears; the
				// creditor is excluded by team_id and never listed regardless of type.
				assert.NoError(t, tx.Create(&[]teamRow{
					{ID: debtor, Name: "Debtor", Type: db_models.SellingTeamType},
					{ID: creditor, Name: "Creditor", Type: db_models.SellingTeamType},
				}).Error)

				count := func() int64 {
					var n int64
					assert.NoError(t, tx.Model(&db_models.OweLimitConfiguration{}).Count(&n).Error)
					return n
				}
				listCustom := func() []*invoice_iface.OweLimitCustomItem {
					res, err := svc.OweLimitCustomList(ctx, connect.NewRequest(&invoice_iface.OweLimitCustomListRequest{
						TeamId: creditor,
						Page:   &common.PageFilter{Page: 1, Limit: 20},
					}))
					assert.NoError(t, err)
					return res.Msg.GetItems()
				}
				getDefault := func() *invoice_iface.OweLimitDefaultGetResponse {
					res, err := svc.OweLimitDefaultGet(ctx, connect.NewRequest(&invoice_iface.OweLimitDefaultGetRequest{
						TeamId: creditor,
					}))
					assert.NoError(t, err)
					return res.Msg
				}

				t.Run("default absent → not configured", func(t *testing.T) {
					assert.False(t, getDefault().GetConfigured())
				})

				t.Run("default set creates, re-set updates in place", func(t *testing.T) {
					_, err := svc.OweLimitDefaultSet(ctx, connect.NewRequest(&invoice_iface.OweLimitDefaultSetRequest{
						TeamId: creditor, Threshold: 100,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(1), count())
					assert.True(t, getDefault().GetConfigured())
					assert.Equal(t, float64(100), getDefault().GetThreshold())

					// Re-set is an update, not a second row.
					_, err = svc.OweLimitDefaultSet(ctx, connect.NewRequest(&invoice_iface.OweLimitDefaultSetRequest{
						TeamId: creditor, Threshold: 250,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(1), count())
					assert.Equal(t, float64(250), getDefault().GetThreshold())
				})

				t.Run("custom upsert is idempotent, list excludes the default row", func(t *testing.T) {
					_, err := svc.OweLimitCustomSet(ctx, connect.NewRequest(&invoice_iface.OweLimitCustomSetRequest{
						TeamId: creditor, ForTeamId: debtor, Threshold: 30,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(2), count()) // default + custom

					// Upsert again → still one custom row, new threshold.
					_, err = svc.OweLimitCustomSet(ctx, connect.NewRequest(&invoice_iface.OweLimitCustomSetRequest{
						TeamId: creditor, ForTeamId: debtor, Threshold: 45,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(2), count())

					items := listCustom()
					assert.Len(t, items, 1) // only the debtor selling team (creditor excluded)
					assert.Equal(t, debtor, items[0].GetForTeamId())
					assert.Equal(t, float64(45), items[0].GetThreshold()) // custom beats default
				})

				t.Run("custom beats default in EvaluateOweLimits", func(t *testing.T) {
					// Debt 50: allowed under the default (250) but blocked by the custom (45).
					assert.NoError(t, tx.Create(&invoice_models.TeamBalance{
						TeamID: debtor, ForTeamID: creditor,
						BalanceType: invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE,
						Balance:     -50,
					}).Error)

					res, err := invoice_v2.EvaluateOweLimits(tx, debtor, []uint64{creditor})
					assert.NoError(t, err)
					assert.False(t, res[creditor].GetAllow())
					assert.Equal(t, float64(45), res[creditor].GetThreshold())
					assert.Equal(t, float64(50), res[creditor].GetActiveAmount())
				})

				t.Run("custom delete falls back to the default", func(t *testing.T) {
					_, err := svc.OweLimitCustomDelete(ctx, connect.NewRequest(&invoice_iface.OweLimitCustomDeleteRequest{
						TeamId: creditor, ForTeamId: debtor,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(1), count()) // only the default remains

					// The debtor still appears, now resolving to the creditor's default (250).
					items := listCustom()
					assert.Len(t, items, 1)
					assert.Equal(t, debtor, items[0].GetForTeamId())
					assert.Equal(t, float64(250), items[0].GetThreshold())

					// Now the default (250) governs: debt 50 is under it → allowed.
					res, err := invoice_v2.EvaluateOweLimits(tx, debtor, []uint64{creditor})
					assert.NoError(t, err)
					assert.True(t, res[creditor].GetAllow())
					assert.Equal(t, float64(250), res[creditor].GetThreshold())
				})

				t.Run("custom for self is rejected", func(t *testing.T) {
					_, err := svc.OweLimitCustomSet(ctx, connect.NewRequest(&invoice_iface.OweLimitCustomSetRequest{
						TeamId: creditor, ForTeamId: creditor, Threshold: 10,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}

// TestOweLimitCustomListSortByThreshold verifies the list orders selling
// counterparties by resolved threshold (custom over default) highest-first, with
// no-limit teams (threshold 0) sorted among the zeros.
func TestOweLimitCustomListSortByThreshold(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "owe limit custom list sort",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&db_models.OweLimitConfiguration{},
					&teamRow{},
				))

				const creditor = uint64(10)
				teamID := func(id uint64) *uint64 { return &id }

				// Four selling counterparties of the creditor, which has a default rule of
				// 120. Beta has a custom 300 and Delta a custom 50 (both beat the default);
				// Acme and Zeta have no custom row, so both fall back to the default 120.
				assert.NoError(t, tx.Create(&[]teamRow{
					{ID: 2, Name: "Beta", Type: db_models.SellingTeamType},
					{ID: 3, Name: "Acme", Type: db_models.SellingTeamType},
					{ID: 4, Name: "Delta", Type: db_models.SellingTeamType},
					{ID: 5, Name: "Zeta", Type: db_models.SellingTeamType},
					{ID: creditor, Name: "Creditor", Type: db_models.SellingTeamType},
					{ID: 6, Name: "Warehouse", Type: db_models.WarehouseTeamType}, // wrong type, never listed
				}).Error)
				assert.NoError(t, tx.Create(&[]db_models.OweLimitConfiguration{
					{TeamID: creditor, IsDefault: true, Threshold: 120},
					{TeamID: creditor, ForTeamID: teamID(2), Threshold: 300},
					{TeamID: creditor, ForTeamID: teamID(4), Threshold: 50},
				}).Error)

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				list := func(req *invoice_iface.OweLimitCustomListRequest) []*invoice_iface.OweLimitCustomItem {
					req.TeamId = creditor
					req.Page = &common.PageFilter{Page: 1, Limit: 20}
					res, err := svc.OweLimitCustomList(ctx, connect.NewRequest(req))
					assert.NoError(t, err)
					return res.Msg.GetItems()
				}
				ids := func(items []*invoice_iface.OweLimitCustomItem) []uint64 {
					out := make([]uint64, len(items))
					for i, it := range items {
						out[i] = it.GetForTeamId()
					}
					return out
				}

				t.Run("defaults to threshold desc, resolving custom over default", func(t *testing.T) {
					items := list(&invoice_iface.OweLimitCustomListRequest{})
					assert.Len(t, items, 4) // creditor and the warehouse team excluded

					// Beta(300), then Acme & Zeta tie at the default 120 (name breaks the
					// tie → Acme before Zeta), then Delta(50).
					assert.Equal(t, []uint64{2, 3, 5, 4}, ids(items))
					thresholds := make([]float64, len(items))
					for i, it := range items {
						thresholds[i] = it.GetThreshold()
					}
					assert.Equal(t, []float64{300, 120, 120, 50}, thresholds)
				})

				t.Run("sort threshold asc", func(t *testing.T) {
					items := list(&invoice_iface.OweLimitCustomListRequest{
						Sort: &invoice_iface.OweLimitCustomSort{
							Type:     invoice_iface.OweLimitCustomSortType_OWE_LIMIT_CUSTOM_SORT_TYPE_THRESHOLD,
							SortType: invoice_iface.SortType_SORT_TYPE_ASC,
						},
					})
					assert.Equal(t, []uint64{4, 3, 5, 2}, ids(items))
				})

				t.Run("filter q matches the debtor name", func(t *testing.T) {
					items := list(&invoice_iface.OweLimitCustomListRequest{
						Filter: &invoice_iface.OweLimitCustomListFilter{Q: "et"}, // Beta, Zeta
					})
					assert.Equal(t, []uint64{2, 5}, ids(items))
				})
			})
		},
	)
}
