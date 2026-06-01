package invoice_models

import "time"

type InvoiceLog struct {
	ID         uint64 `gorm:"primarykey"`
	FromTeamID uint64
	ToTeamID   uint64
	ActorID    uint64
	LogType    uint8
	Amount     float64
	Balance    float64
	CreatedAt  time.Time
}
