package invoice_models

import (
	"time"

	"github.com/pdcgo/schema/services/invoice_iface/v2"
)

// Payment is a settlement between two teams: team_id pays for_team_id. It starts
// PENDING on create and is moved to ACCEPTED / REJECTED via the accept/reject
// RPCs (which also record who completed it).
type Payment struct {
	ID            uint64 `gorm:"primaryKey"`
	TeamID        uint64 `gorm:"index;not null"`
	ForTeamID     uint64 `gorm:"index;not null"`
	DocumentID    string
	Amount        float64 `gorm:"not null"`
	Note          string
	Status        invoice_iface.PaymentStatus `gorm:"index;not null"`
	CreatedByID   uint64                      `gorm:"not null"`
	CompletedByID *uint64

	CreatedAt  time.Time `gorm:"not null"`
	AcceptedAt *time.Time
	RejectedAt *time.Time
	UpdatedAt  time.Time `gorm:"not null"`
}
