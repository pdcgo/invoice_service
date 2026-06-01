package invoice_models

import "time"

type Balance struct {
	ID        uint64    `gorm:"primarykey"`
	TeamID    uint64    `json:"team_id"`
	ToTeamID  uint64    `json:"to_team_id"`
	Balance   float64   `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
