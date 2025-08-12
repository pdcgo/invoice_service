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
	CreatedAt(timeMin, timeMax time.Time) PaymentHistoryQuery
	JoinInvoice(clauseJoin string) InvoiceQuery
	GetQuery() *gorm.DB
}

type paymentHistoryQueryImpl struct {
	tx *gorm.DB

	joinInvoice bool
}

// GetQuery implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) GetQuery() *gorm.DB {
	return p.tx
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

// JoinInvoice implements PaymentHistoryQuery.
func (p *paymentHistoryQueryImpl) JoinInvoice(clauseJoin string) InvoiceQuery {
	invoiceQuery := &invoiceQueryImpl{
		joinPaymentHistories: true,
	}

	if p.joinInvoice {
		invoiceQuery.tx = p.tx
		return invoiceQuery
	}

	joinQuery := "JOIN invoices ON invoices.hist_id = payment_histories.id"

	clauseJoin, _ = strings.CutSuffix(clauseJoin, "JOIN")
	if clauseJoin != "" {
		joinQuery = fmt.Sprintf("%s %s", clauseJoin, joinQuery)
	}

	p.tx = p.tx.Joins(joinQuery)
	p.joinInvoice = true

	invoiceQuery.tx = p.tx
	return invoiceQuery
}
