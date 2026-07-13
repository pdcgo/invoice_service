package invoice_v2_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
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
				))

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()

				const creditor = uint64(8) // the limit owner
				const debtor = uint64(1)

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
					assert.Len(t, items, 1) // the default row is NOT listed
					assert.Equal(t, debtor, items[0].GetForTeamId())
					assert.Equal(t, float64(45), items[0].GetThreshold())
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
					assert.Len(t, listCustom(), 0)

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
