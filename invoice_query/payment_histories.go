package invoice_query

import (
	"fmt"
	"strings"
	"time"

	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NewPaymentHistoryQuery(tx *gorm.DB, lock bool) PaymentHistoryQuery {
	if lock {
		tx = tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "NOWAIT",
		})
	}
	return &paymentHistoryQueryImpl{
		tx: tx.Model(&db_models.PaymentHistory{}),
	}
}

type PaymentHistoryQuery interface {
	FromTeam(teamID uint) PaymentHistoryQuery
	FromTeamType(teamType db_models.TeamType) PaymentHistoryQuery
	ToTeam(teamID uint) PaymentHistoryQuery
	ToTeamType(teamType db_models.TeamType) PaymentHistoryQuery
	InvoiceStatus(status db_models.InvoiceStatus) PaymentHistoryQuery
	CreatedAt(timeMin, timeMax time.Time) PaymentHistoryQuery
	JoinInvoice(joinClause string) PaymentHistoryQuery
	JoinFromTeam(joinClause string) PaymentHistoryQuery
	JoinToTeam(joinClause string) PaymentHistoryQuery
	HasJoinInvoice() bool
	HasJoinFromTeam() bool
	HasJoinToTeam() bool
	GetQuery() *gorm.DB
}

type paymentHistoryQueryImpl struct {
	tx *gorm.DB

	hasJoinInvoice  bool
	hasJoinFromTeam bool
	hasJoinToTeam   bool
}

// GetQuery implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) GetQuery() *gorm.DB {
	return p.tx
}

func (p *paymentHistoryQueryImpl) FromTeam(teamID uint) PaymentHistoryQuery {
	if teamID != 0 {
		p.JoinInvoice("JOIN")
		p.tx = p.tx.Where("invoices.from_team_id = ?", teamID)
	}

	return p
}

func (p *paymentHistoryQueryImpl) FromTeamType(teamType db_models.TeamType) PaymentHistoryQuery {
	if teamType != "" {
		p.JoinFromTeam("JOIN")
		p.tx = p.tx.Where("from_team.type = ?", teamType)
	}

	return p
}

func (p *paymentHistoryQueryImpl) ToTeam(teamID uint) PaymentHistoryQuery {
	if teamID != 0 {
		p.JoinInvoice("JOIN")
		p.tx = p.tx.Where("invoices.to_team_id = ?", teamID)
	}

	return p
}

func (p *paymentHistoryQueryImpl) ToTeamType(teamType db_models.TeamType) PaymentHistoryQuery {
	if teamType != "" {
		p.JoinToTeam("JOIN")
		p.tx = p.tx.Where("to_team.type = ?", teamType)
	}

	return p
}

func (p *paymentHistoryQueryImpl) InvoiceStatus(status db_models.InvoiceStatus) PaymentHistoryQuery {
	if status != "" {
		p.JoinInvoice("JOIN")
		p.tx = p.tx.Where("invoices.status = ?", status)
	}

	return p
}

func (p *paymentHistoryQueryImpl) CreatedAt(timeMin, timeMax time.Time) PaymentHistoryQuery {
	if !timeMin.IsZero() {
		p.tx = p.tx.Where("payment_histories.created_at >= ?", timeMin)
	}
	if !timeMax.IsZero() {
		p.tx = p.tx.Where("payment_histories.created_at <= ?", timeMax)
	}
	return p
}

func (p *paymentHistoryQueryImpl) JoinInvoice(joinClause string) PaymentHistoryQuery {
	if p.hasJoinInvoice {
		return p
	}

	joinQuery := "JOIN invoices ON invoices.hist_id = payment_histories.id"

	joinClause, _ = strings.CutSuffix(joinClause, "JOIN")
	if joinClause != "" {
		joinQuery = fmt.Sprintf("%s %s", joinClause, joinQuery)
	}

	p.tx = p.tx.Joins(joinQuery)
	p.hasJoinInvoice = true

	return p
}

// JoinFromTeam implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) JoinFromTeam(joinClause string) PaymentHistoryQuery {
	if !p.hasJoinInvoice {
		p.JoinInvoice("JOIN")
	}

	joinQuery := "JOIN teams AS from_team ON from_team.id = invoices.from_team_id"

	joinClause, _ = strings.CutSuffix(joinClause, "JOIN")
	if joinClause != "" {
		joinQuery = fmt.Sprintf("%s %s", joinClause, joinQuery)
	}

	p.tx = p.tx.Joins(joinQuery)
	p.hasJoinFromTeam = true
	return p
}

// JoinToTeam implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) JoinToTeam(joinClause string) PaymentHistoryQuery {
	if !p.hasJoinInvoice {
		p.JoinInvoice("JOIN")
	}

	joinQuery := "JOIN teams AS to_team ON to_team.id = invoices.to_team_id"

	joinClause, _ = strings.CutSuffix(joinClause, "JOIN")
	if joinClause != "" {
		joinQuery = fmt.Sprintf("%s %s", joinClause, joinQuery)
	}

	p.tx = p.tx.Joins(joinQuery)
	p.hasJoinToTeam = true
	return p
}

// HasJoinInvoice implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) HasJoinInvoice() bool {
	return p.hasJoinInvoice
}

// HasJoinInvoice implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) HasJoinFromTeam() bool {

	return p.hasJoinFromTeam
}

// HasJoinInvoice implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) HasJoinToTeam() bool {
	return p.hasJoinToTeam
}
