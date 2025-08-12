package invoice_query

import (
	"fmt"
	"strings"

	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NewInvoiceQuery(tx *gorm.DB, lock bool) InvoiceQuery {
	if lock {
		tx = tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "NOWAIT",
		})
	}

	return &invoiceQueryImpl{
		tx: tx.Model(&db_models.Invoice{}),
	}
}

type InvoiceQuery interface {
	WithIDs(invoiceIDs []uint) InvoiceQuery
	FromTeam(teamID uint) InvoiceQuery
	FromTeamType(teamType db_models.TeamType) InvoiceQuery
	JoinFromTeam(clauseJoin string) InvoiceQuery
	HasJoinFromTeam() bool
	ToTeam(teamID uint) InvoiceQuery
	ToTeamType(teamType db_models.TeamType) InvoiceQuery
	JoinToTeam(clauseJoin string) InvoiceQuery
	HasJoinToTeam() bool
	Status(status db_models.InvoiceStatus) InvoiceQuery
	Statuses(statuses []db_models.InvoiceStatus) InvoiceQuery
	ExcludeStatuses(statuses []db_models.InvoiceStatus) InvoiceQuery
	WithType(invoiceType db_models.InvoiceType) InvoiceQuery
	HasSubmission(submissionType SubmissionType) InvoiceQuery
	JoinPaymentSubmission(clauseJoin string) PaymentSubmissionQuery
	JoinPaymentHistories(clauseJoin string) PaymentHistoryQuery
	GetQuery() *gorm.DB
}

type invoiceQueryImpl struct {
	tx *gorm.DB

	joinPaymentSubmission bool
	joinSubmission        bool
	joinPaymentHistories  bool

	joinFromTeam bool
	joinToTeam   bool
}

// GetQuery implements InvoiceQuery.
func (i *invoiceQueryImpl) GetQuery() *gorm.DB {
	return i.tx
}

func (i *invoiceQueryImpl) WithIDs(invoiceIDs []uint) InvoiceQuery {
	if len(invoiceIDs) != 0 {
		i.tx = i.tx.Where("invoices.id IN (?)", invoiceIDs)
	}
	return i
}

func (i *invoiceQueryImpl) FromTeam(teamID uint) InvoiceQuery {
	if teamID != 0 {
		i.tx = i.tx.Where("invoices.from_team_id = ?", teamID)
	}
	return i
}
func (i *invoiceQueryImpl) FromTeamType(teamType db_models.TeamType) InvoiceQuery {
	if teamType != "" {
		i.JoinFromTeam("JOIN")
		i.tx = i.tx.Where("from_team.type = ?", teamType)
	}
	return i
}
func (i *invoiceQueryImpl) HasJoinFromTeam() bool {
	return i.joinFromTeam
}
func (i *invoiceQueryImpl) ToTeam(teamID uint) InvoiceQuery {
	if teamID != 0 {
		i.tx = i.tx.Where("invoices.to_team_id = ?", teamID)
	}
	return i
}
func (i *invoiceQueryImpl) ToTeamType(teamType db_models.TeamType) InvoiceQuery {
	if teamType != "" {
		i.JoinToTeam("JOIN")
		i.tx = i.tx.Where("to_team.type = ?", teamType)
	}
	return i
}
func (i *invoiceQueryImpl) HasJoinToTeam() bool {
	return i.joinToTeam
}
func (i *invoiceQueryImpl) Status(status db_models.InvoiceStatus) InvoiceQuery {
	if status != "" {
		i.tx = i.tx.Where("invoices.status = ?", status)
	}
	return i
}
func (i *invoiceQueryImpl) Statuses(statuses []db_models.InvoiceStatus) InvoiceQuery {
	if len(statuses) != 0 {
		i.tx = i.tx.Where("invoices.status IN (?)", statuses)
	}
	return i
}
func (i *invoiceQueryImpl) ExcludeStatuses(statuses []db_models.InvoiceStatus) InvoiceQuery {
	if len(statuses) != 0 {
		i.tx = i.tx.Where("invoices.status NOT IN (?)", statuses)
	}
	return i
}
func (i *invoiceQueryImpl) WithType(invoiceType db_models.InvoiceType) InvoiceQuery {
	if invoiceType != "" {
		i.tx = i.tx.Where("invoices.type = ?", invoiceType)
	}
	return i
}

type SubmissionType string

const (
	HaveSubmission SubmissionType = "have_submission"
	NoSubmission   SubmissionType = "no_submission"
)

func (SubmissionType) EnumList() []string {
	return []string{
		"have_submission",
		"no_submission",
	}
}

func (i *invoiceQueryImpl) HasSubmission(submissionType SubmissionType) InvoiceQuery {
	switch submissionType {
	case HaveSubmission:
		i.tx = i.tx.Where("invoices.has_submission = ?", true)
	case NoSubmission:
		i.tx = i.tx.Where("invoices.has_submission = ?", false)
	}
	return i
}
func (i *invoiceQueryImpl) JoinSubmission(clauseJoin string) InvoiceQuery {
	if i.joinSubmission {
		return i
	}

	joinQuery := "JOIN invoice_payment_submission ON invoice_payment_submission.invoice_id = invoices.id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinSubmission = true

	return i
}
func (i *invoiceQueryImpl) JoinPaymentSubmission(clauseJoin string) PaymentSubmissionQuery {
	submissionQuery := &paymentSubmissionQueryImpl{
		joinInvoice:           true,
		joinInvoiceSubmission: true,
	}

	if i.joinPaymentSubmission {
		submissionQuery.tx = i.tx
		return submissionQuery
	}

	if !i.joinSubmission {
		i.JoinSubmission("JOIN")
	}

	joinQuery := "JOIN payment_submissions ON payment_submissions.id = invoice_payment_submission.payment_submission_id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinPaymentSubmission = true

	submissionQuery.tx = i.tx
	return submissionQuery
}

func (i *invoiceQueryImpl) JoinFromTeam(clauseJoin string) InvoiceQuery {
	if i.joinFromTeam {
		return i
	}

	joinQuery := "JOIN teams AS from_team ON from_team.id = invoices.from_team_id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinFromTeam = true

	return i
}
func (i *invoiceQueryImpl) JoinToTeam(clauseJoin string) InvoiceQuery {
	if i.joinToTeam {
		return i
	}

	joinQuery := "JOIN teams AS to_team ON to_team.id = invoices.to_team_id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinToTeam = true

	return i
}

// JoinPaymentHistories implements InvoiceQuery.
func (i *invoiceQueryImpl) JoinPaymentHistories(clauseJoin string) PaymentHistoryQuery {
	paymentHistoryQuery := &paymentHistoryQueryImpl{
		joinInvoice: true,
	}
	if i.joinPaymentHistories {
		paymentHistoryQuery.tx = i.tx
		return paymentHistoryQuery
	}

	joinQuery := "JOIN payment_histories ON payment_histories.id = invoices.hist_id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinPaymentHistories = true

	paymentHistoryQuery.tx = i.tx
	return paymentHistoryQuery
}
