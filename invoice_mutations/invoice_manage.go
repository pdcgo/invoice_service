package invoice_mutations

import (
	"errors"
	"fmt"
	"time"

	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/interfaces/identity_iface"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InvoiceQuery interface {
	To(toID uint) InvoiceQuery
	From(fromID uint) InvoiceQuery
	Type(tipe db_models.InvoiceType) InvoiceQuery
	TxID(txID uint) InvoiceQuery
	getQuery() *gorm.DB
}

type ReadjustAmountPayload struct {
	FromID  uint
	ToID    uint
	TxID    *uint
	OrderID *uint
	Amount  float64
}

type InvoiceManage interface {
	Get(query InvoiceQuery) InvoiceManage
	ReadjustAmount(pay *ReadjustAmountPayload) InvoiceManage
	Err() error
}

type invoiceQueryImpl struct {
	query *gorm.DB
}

func NewInvoiceQuery(tx *gorm.DB, lock bool) InvoiceQuery {
	if lock {
		tx = tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "NOWAIT",
		})
	}
	return &invoiceQueryImpl{
		query: tx.Model(&db_models.Invoice{}),
	}
}

// getQuery implements InvoiceQuery.
func (i *invoiceQueryImpl) getQuery() *gorm.DB {
	return i.query
}

// From implements InvoiceQuery.
func (i *invoiceQueryImpl) From(fromID uint) InvoiceQuery {
	if fromID != 0 {
		i.query = i.query.Where("from_team_id = ?", fromID)
	}
	return i
}

// To implements InvoiceQuery.
func (i *invoiceQueryImpl) To(toID uint) InvoiceQuery {
	if toID != 0 {
		i.query = i.query.Where("to_team_id = ?", toID)
	}
	return i
}

// TxID implements InvoiceQuery.
func (i *invoiceQueryImpl) TxID(txID uint) InvoiceQuery {
	if txID != 0 {
		i.query = i.query.Where("tx_id = ?", txID)
	}
	return i
}

// Type implements InvoiceQuery.
func (i *invoiceQueryImpl) Type(tipe db_models.InvoiceType) InvoiceQuery {
	if tipe != "" {
		i.query = i.query.Where("type = ?", tipe)
	}
	return i
}

type invoiceManageImpl struct {
	invoices []*db_models.Invoice
	tx       *gorm.DB
	agent    identity_iface.Agent
	query    InvoiceQuery
	err      error
}

func NewInvoiceManage(tx *gorm.DB, agent identity_iface.Agent) InvoiceManage {
	return &invoiceManageImpl{
		tx:    tx,
		agent: agent,
	}
}

// Err implements InvoiceManage.
func (i *invoiceManageImpl) Err() error {
	return i.err
}

// Get implements InvoiceManage.
func (i *invoiceManageImpl) Get(query InvoiceQuery) InvoiceManage {
	i.invoices = []*db_models.Invoice{}
	i.query = query
	tx := query.getQuery()
	err := tx.Find(&i.invoices).Error

	if err != nil {
		return i.setErr(err)
	}
	return i
}

// ReadjustAmount implements InvoiceManage.
func (i *invoiceManageImpl) ReadjustAmount(pay *ReadjustAmountPayload) InvoiceManage {
	amount := pay.Amount

	if (pay.OrderID == nil) && (pay.TxID == nil) {
		return i.setErr(errors.New("orderID or txID must set"))
	}

	for _, invo := range i.invoices {
		if invo.FromTeamID != pay.FromID {
			return i.setErr(fmt.Errorf("from team in invoice error %d,%d", invo.FromTeamID, pay.FromID))
		}
		if invo.ToTeamID != pay.ToID {
			return i.setErr(fmt.Errorf("to team in invoice error %d,%d", invo.ToTeamID, pay.ToID))
		}
		switch invo.Status {
		case db_models.InvoicePaid:
			amount -= invo.Amount
		case db_models.InvoiceNotFinal, db_models.InvoiceNotPaid:
			invo.Status = db_models.InvoiceCancel

			err := i.tx.Save(invo).Error
			if err != nil {
				return i.setErr(err)
			}
		}
	}

	if amount == 0 {
		return i
	}

	var fromID, toID uint
	if amount > 0 {
		fromID = pay.FromID
		toID = pay.ToID
	} else {
		fromID = pay.ToID
		toID = pay.FromID
		amount = amount * -1
	}

	invo := db_models.Invoice{
		TxID:       pay.TxID,
		OrderID:    pay.OrderID,
		FromTeamID: fromID,
		ToTeamID:   toID,
		Status:     db_models.InvoiceNotPaid,
		Amount:     amount,
		Type:       db_models.InvoShipFeeType,
		Created:    time.Now(),
	}

	err := i.tx.Save(&invo).Error
	if err != nil {
		return i.setErr(err)
	}

	return i
}

func (i *invoiceManageImpl) setErr(err error) *invoiceManageImpl {
	if i.err != nil {
		return i
	}

	if err != nil {
		i.err = err
	}

	return i
}
