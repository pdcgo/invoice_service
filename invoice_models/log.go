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

// BalanceChangeOrderSource attributes a BalanceChangeLog leg to the order that
// caused it (order-driven fee posts: product_fee / warehouse_fee). One row per
// ledger leg (PK = balance_change_log_id), so both the primary and mirror legs —
// and their cancel reversals — are queryable by order. OrderSystem disambiguates
// the shared numeric id-space between legacy orders and v3 orders. Purely
// scope/filter; idempotency is enforced upstream per writer.
type BalanceChangeOrderSource struct {
	BalanceChangeLogID uint64                    `gorm:"primaryKey"`
	OrderSystem        invoice_iface.OrderSystem `gorm:"not null"`
	OrderID            uint64                    `gorm:"index:idx_bcos_order;not null"`
	OrderItemID        uint64                    // 0 when the fee is not attributable to one item
	TeamID             uint64                    `gorm:"not null"` // ordering team, canonical across legs
	WarehouseID        uint64                    `gorm:"index;not null"`
	CreatedAt          time.Time                 `gorm:"not null"`
}

// BalanceChangeRestockSource attributes a BalanceChangeLog leg to the inventory
// transaction that caused it (cod_fee, posted on RestockAccepted). Same shape and
// contract as BalanceChangeOrderSource — one row per ledger leg — but keyed by the
// inv_transactions id the event carries, so there is no order system to
// disambiguate. Purely scope/filter; idempotency is enforced upstream per writer.
type BalanceChangeRestockSource struct {
	BalanceChangeLogID uint64    `gorm:"primaryKey"`
	TxID               uint64    `gorm:"index;not null"`
	TxItemID           uint64    // 0 when the fee is transaction-level (cod fee always is)
	TeamID             uint64    `gorm:"not null"`
	WarehouseID        uint64    `gorm:"index;not null"`
	CreatedAt          time.Time `gorm:"not null"`
}

// BalanceChangeBrokenSource attributes a BalanceChangeLog leg to the inventory
// transaction that caused it (stock_problem). Mirrors BalanceChangeRestockSource;
// kept separate so the two fee sources stay independently queryable.
type BalanceChangeBrokenSource struct {
	BalanceChangeLogID uint64    `gorm:"primaryKey"`
	TxID               uint64    `gorm:"index;not null"`
	TxItemID           uint64    // 0 when the problem is not attributable to one item
	TeamID             uint64    `gorm:"not null"`
	WarehouseID        uint64    `gorm:"index;not null"`
	CreatedAt          time.Time `gorm:"not null"`
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
