package invoice_query

import (
	"fmt"
	"strings"
	"time"

	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NewPaymentSubmissionQuery(tx *gorm.DB, lock bool) PaymentSubmissionQuery {
	if lock {
		tx = tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "NOWAIT",
		})
	}

	return &paymentSubmissionQueryImpl{
		tx: tx.Model(&db_models.PaymentSubmission{}),
	}
}

type PaymentSubmissionQuery interface {
	WithSubmission(submissionID uint) PaymentSubmissionQuery
	CreatedBy(userID uint) PaymentSubmissionQuery
	VerifyBy(userID uint) PaymentSubmissionQuery
	WithStatus(status db_models.PaymentSubmissionStatus) PaymentSubmissionQuery
	CreatedAt(timeMin, timeMax time.Time) PaymentSubmissionQuery
	JoinInvoice(clauseJoin string) InvoiceQuery
	GetQuery() *gorm.DB
}

type paymentSubmissionQueryImpl struct {
	tx *gorm.DB

	joinInvoice           bool
	joinInvoiceSubmission bool
}

// GetQuery implements PaymentSubmissionQuery.
func (p *paymentSubmissionQueryImpl) GetQuery() *gorm.DB {
	return p.tx
}

func (p *paymentSubmissionQueryImpl) WithSubmission(submissionID uint) PaymentSubmissionQuery {
	if submissionID != 0 {
		p.tx = p.tx.Where("payment_submissions.id = ?", submissionID)
	}
	return p
}
func (p *paymentSubmissionQueryImpl) CreatedBy(userID uint) PaymentSubmissionQuery {
	if userID != 0 {
		p.tx = p.tx.Where("payment_submissions.created_by_id = ?", userID)
	}
	return p
}
func (p *paymentSubmissionQueryImpl) VerifyBy(userID uint) PaymentSubmissionQuery {
	if userID != 0 {
		p.tx = p.tx.Where("payment_submissions.completed_by_id = ?", userID)
	}
	return p
}
func (p *paymentSubmissionQueryImpl) WithStatus(status db_models.PaymentSubmissionStatus) PaymentSubmissionQuery {
	if status != "" {
		p.tx = p.tx.Where("payment_submissions.status = ?", status)
	}
	return p
}

func (p *paymentSubmissionQueryImpl) CreatedAt(timeMin, timeMax time.Time) PaymentSubmissionQuery {
	if !timeMin.IsZero() {
		p.tx = p.tx.Where("payment_submissions.created_at >= ?", timeMin)
	}
	if !timeMax.IsZero() {
		p.tx = p.tx.Where("payment_submissions.created_at <= ?", timeMax)
	}
	return p
}

func (i *paymentSubmissionQueryImpl) JoinInvoiceSubmission(clauseJoin string) PaymentSubmissionQuery {
	if i.joinInvoiceSubmission {
		return i
	}

	joinQuery := "JOIN invoice_payment_submission ON invoice_payment_submission.payment_submission_id = payment_submissions.id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	i.tx = i.tx.Joins(joinQuery)
	i.joinInvoiceSubmission = true

	return i
}

// JoinInvoice implements PaymentSubmissionQuery.
func (p *paymentSubmissionQueryImpl) JoinInvoice(clauseJoin string) InvoiceQuery {
	invoiceQuery := &invoiceQueryImpl{
		joinSubmission:        true,
		joinPaymentSubmission: true,
	}

	if p.joinInvoice {
		invoiceQuery.tx = p.tx
		return invoiceQuery
	}

	if !p.joinInvoiceSubmission {
		p.JoinInvoiceSubmission("JOIN")
	}

	joinQuery := "JOIN invoices ON invoices.id = invoice_payment_submission.invoice_id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	p.tx = p.tx.Joins(joinQuery)
	p.joinInvoice = true

	invoiceQuery.tx = p.tx
	return invoiceQuery
}
