package invoice_v2_test

import (
	"testing"

	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestEvaluateOweLimits(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "evaluate owe limits",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.TeamBalance{},
					&db_models.OweLimitConfiguration{},
				))

				debtor := uint64(1)

				// Config: creditor 8/9 default 100, creditor 10 unlimited (0), creditor 11
				// none, creditor 12 custom (30) beating its default (999).
				cfgs := []*db_models.OweLimitConfiguration{
					{TeamID: 8, IsDefault: true, Threshold: 100},
					{TeamID: 9, IsDefault: true, Threshold: 100},
					{TeamID: 10, IsDefault: true, Threshold: 0},
					{TeamID: 12, IsDefault: true, Threshold: 999},
					{TeamID: 12, IsDefault: false, ForTeamID: &debtor, Threshold: 30},
				}
				assert.NoError(t, tx.Create(&cfgs).Error)

				// Debtor 1's PAYABLE (stored negative) to each creditor.
				pay := invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
				bals := []*invoice_models.TeamBalance{
					{TeamID: 1, ForTeamID: 8, BalanceType: pay, Balance: -50},   // debt 50 < 100
					{TeamID: 1, ForTeamID: 9, BalanceType: pay, Balance: -150},  // debt 150 >= 100
					{TeamID: 1, ForTeamID: 10, BalanceType: pay, Balance: -500}, // unlimited
					{TeamID: 1, ForTeamID: 12, BalanceType: pay, Balance: -40},  // debt 40 >= custom 30
				}
				assert.NoError(t, tx.Create(&bals).Error)

				res, err := invoice_v2.EvaluateOweLimits(tx, debtor, []uint64{8, 9, 10, 11, 12})
				assert.NoError(t, err)

				// creditor 8: debt below default threshold → allow.
				assert.True(t, res[8].GetAllow())
				assert.Equal(t, float64(50), res[8].GetActiveAmount())
				assert.Equal(t, float64(100), res[8].GetThreshold())

				// creditor 9: debt at/above threshold → block.
				assert.False(t, res[9].GetAllow())
				assert.Equal(t, float64(150), res[9].GetActiveAmount())

				// creditor 10: threshold 0 = unlimited → allow despite debt.
				assert.True(t, res[10].GetAllow())

				// creditor 11: no config → allow.
				assert.True(t, res[11].GetAllow())

				// creditor 12: custom (30) beats default (999); debt 40 >= 30 → block.
				assert.False(t, res[12].GetAllow())
				assert.Equal(t, float64(30), res[12].GetThreshold())
			})
		},
	)
}
