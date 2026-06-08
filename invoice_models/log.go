package invoice_models

import (
	"time"

	"github.com/pdcgo/schema/services/invoice_iface/v2"
)

type BalanceChangeLog struct {
	ID           uint64                          `gorm:"primaryKey"`
	TeamID       uint64                          `gorm:"index;not null"`
	ForTeamID    uint64                          `gorm:"index;not null"`
	ChangeType   invoice_iface.BalanceChangeType `gorm:"not null"`
	ChangeAmount float64                         `gorm:"not null"`
	BalanceType  invoice_iface.BalanceType       `gorm:"not null"`
	Balance      float64                         `gorm:"not null"`
	Note         string
	CreatedByID  uint64    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"index;not null"`
}

type TeamBalance struct {
	ID                   uint64                    `gorm:"primaryKey"`
	TeamID               uint64                    `gorm:"index;not null"`
	ForTeamID            uint64                    `gorm:"index;not null"`
	BalanceType          invoice_iface.BalanceType `gorm:"not null"`
	Balance              float64                   `gorm:"not null"`
	PendingPaymentAmount float64                   `gorm:"not null"`
	CreatedAt            time.Time                 `gorm:"not null"`
	UpdatedAt            time.Time                 `gorm:"not null"`
}

type TeamBalanceDailyLog struct {
	Day          time.Time                 `gorm:"index;not null"`
	TeamID       uint64                    `gorm:"index;not null"`
	ForTeamID    uint64                    `gorm:"index;not null"`
	BalanceType  invoice_iface.BalanceType `gorm:"not null"`
	StartBalance float64                   `gorm:"not null"`
	EndBalance   float64                   `gorm:"not null"`
	ChangeAmount float64                   `gorm:"not null"`
	UpdatedAt    time.Time                 `gorm:"not null"`
	CreatedAt    time.Time                 `gorm:"not null"`
}
