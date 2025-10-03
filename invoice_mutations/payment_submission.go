package invoice_mutations

import (
	"errors"
	"fmt"
	"time"

	"github.com/pdcgo/invoice_service/invoice_query"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/interfaces/identity_iface"
	"gorm.io/gorm"
)

func NewPaymentSubmissionMutation(tx *gorm.DB, agent identity_iface.Agent) PaymentSubmissionMutation {
	return &paymentSubmissionMutationImpl{
		tx:    tx,
		agent: agent,
	}
}

type Queries interface {
	GetQuery() *gorm.DB
}

type PaymentSubmissionMutation interface {
	GetFromQuery(query invoice_query.PaymentSubmissionQuery) (PaymentSubmissionMutation, error)
	CreateSubmissionFromInvoice(invoiceQuery invoice_query.InvoiceQuery, receiptFile string) error
	CreateSubmission(invoiceQuery invoice_query.InvoiceQuery, receiptFile string, amount float64) error
	AcceptSubmission() error
	RejectSubmission() error

	GetData() *db_models.PaymentSubmission
}

type paymentSubmissionMutationImpl struct {
	tx *gorm.DB

	agent identity_iface.Agent

	data *db_models.PaymentSubmission
}

// GetData implements PaymentSubmissionMutation.
func (p *paymentSubmissionMutationImpl) GetData() *db_models.PaymentSubmission {
	return p.data
}

// CreateSubmissionFromInvoice implements PaymentSubmissionMutation.
func (p *paymentSubmissionMutationImpl) CreateSubmissionFromInvoice(invoiceQuery invoice_query.InvoiceQuery, receiptFile string) error {
	return p.CreateSubmission(invoiceQuery, receiptFile, 0)
}

// CreateSubmission implements PaymentSubmissionMutation.
func (p *paymentSubmissionMutationImpl) CreateSubmission(invoiceQuery invoice_query.InvoiceQuery, receiptFile string, amount float64) error {
	var err error

	invoices := InvoiceList{}
	err = invoiceQuery.
		GetQuery().
		Order("invoices.created ASC").
		Find(&invoices).Error
	if err != nil {
		return err
	}

	if amount == 0 {
		amount = invoices.GetAmount()
	}

	invoiceSubmissions := []*db_models.PSubmissionInv{}
	err = p.tx.
		Model(&db_models.PSubmissionInv{}).
		Joins("JOIN payment_submissions ON payment_submissions.id = invoice_payment_submission.payment_submission_id").
		Where("invoice_id IN ?", invoices.GetIDs()).
		Where("payment_submissions.status = ?", db_models.PaymentSubmissionStatusSubmitted).
		Find(&invoiceSubmissions).
		Error
	if err != nil {
		return err
	}

	activeSubmissionMap := map[uint]bool{}
	for _, dd := range invoiceSubmissions {
		activeSubmissionMap[dd.InvoiceID] = true
	}

	newInvoices := []*db_models.Invoice{}
	total := float64(0)

	for _, dd := range invoices {
		invo := dd

		switch invo.Status {
		case db_models.InvoiceNotFinal:
			return fmt.Errorf("%d invoice not final", invo.ID)
		case db_models.InvoiceNotPaid:
			if activeSubmissionMap[dd.ID] {
				continue
			}

			total += dd.Amount
			if total > amount {
				return errors.New("invalid payment amount")
			}

			newInvoices = append(newInvoices, invo)

		case db_models.InvoicePaid:
			return fmt.Errorf("%d invoice have paid", invo.ID)
		}

		if total == amount {
			break
		}
	}

	if total < amount {
		err := errors.New("invalid payment amount")
		return err
	}

	if len(newInvoices) == 0 {
		err := errors.New("doesn't have unpaid invoices")
		return err
	}

	paymentSubmission := db_models.PaymentSubmission{
		CreatedByID: p.agent.GetUserID(),
		Status:      db_models.PaymentSubmissionStatusSubmitted,
		CreatedAt:   time.Now(),
		Receipt:     receiptFile,
		Amount:      amount,
	}

	err = p.tx.Save(&paymentSubmission).Error
	if err != nil {
		return err
	}

	p.data = &paymentSubmission

	for _, inv := range newInvoices {
		inv.HasSubmission = true
		inv.PaymentSubmissions = append(inv.PaymentSubmissions, &paymentSubmission)

		err := p.tx.Save(inv).Error
		if err != nil {
			return err
		}
	}

	err = p.CreateLog(paymentSubmission.ID, db_models.PaymentSubmissionStatusSubmitted)
	if err != nil {
		return err
	}

	return nil
}

func (p *paymentSubmissionMutationImpl) GetFromQuery(query invoice_query.PaymentSubmissionQuery) (PaymentSubmissionMutation, error) {
	p.data = &db_models.PaymentSubmission{}

	sqlQuery := query.GetQuery()
	err := sqlQuery.
		Find(&p.data).Error
	if err != nil {
		return p, err
	}
	if p.data.ID == 0 {
		err := errors.New("payment submission not found")
		return p, err
	}

	return p, nil
}

type InvoiceList []*db_models.Invoice

func (d InvoiceList) GetIDs() []uint {
	hasil := []uint{}

	for _, item := range d {
		hasil = append(hasil, item.ID)
	}

	return hasil
}

func (d InvoiceList) GetAmount() float64 {
	var amount float64 = 0
	for _, item := range d {
		amount += item.Amount
	}

	return amount
}

func (p *paymentSubmissionMutationImpl) AcceptSubmission() error {
	var err error

	if p.data == nil {
		err = errors.New("data payment submission not initialized")
		return err
	}

	invoices := []*db_models.Invoice{}
	err = p.tx.Model(&db_models.Invoice{}).
		Joins("JOIN invoice_payment_submission ON invoice_payment_submission.invoice_id = invoices.id").
		Where("invoice_payment_submission.payment_submission_id = ?", p.data.ID).
		Find(&invoices).Error
	if err != nil {
		return err
	}

	adjustmentInvoice := InvoiceList{}
	setPaidInvoice := InvoiceList{}
	history := db_models.PaymentHistory{
		Amount:      p.data.Amount,
		CreatedByID: p.agent.GetUserID(),
		CreatedAt:   time.Now(),
	}

	for _, item := range invoices {
		switch item.Status {
		case db_models.InvoiceNotFinal:
			return fmt.Errorf("%d invoice not final", item.ID)

		case db_models.InvoiceNotPaid:
			setPaidInvoice = append(setPaidInvoice, item)

			switch item.Type {
			case db_models.InvoWarehouseAdjustment, db_models.InvoProductAdjustment, db_models.InvoCommonAdjustment:
				continue
			}

			if item.NeedAdj {
				adjustmentInvoice = append(adjustmentInvoice, item)
			}

		case db_models.InvoicePaid:
			return fmt.Errorf("%d invoice have paid", item.ID)
		}
	}

	err = p.UpdateStatus(db_models.PaymentSubmissionStatusAccepted)
	if err != nil {
		return err
	}

	err = p.tx.Save(&history).Error
	if err != nil {
		return err
	}

	err = p.tx.Model(&db_models.Invoice{}).
		Where("id IN ?", setPaidInvoice.GetIDs()).
		Where("status = ?", db_models.InvoiceNotPaid).
		Updates(map[string]interface{}{
			"hist_id":     history.ID,
			"status":      db_models.InvoicePaid,
			"paid_at":     p.data.CreatedAt,
			"accepted_at": time.Now(),
		}).Error
	if err != nil {
		return err
	}

	err = p.CreateAdjustment(adjustmentInvoice...)
	if err != nil {
		return err
	}

	return nil
}

func (p *paymentSubmissionMutationImpl) RejectSubmission() error {
	var err error

	err = p.UpdateStatus(db_models.PaymentSubmissionStatusRejected)
	if err != nil {
		return err
	}

	subQuery := p.tx.
		Table("invoice_payment_submission").
		Where("invoice_payment_submission.payment_submission_id = ?", p.data.ID)

	err = p.tx.Model(&db_models.Invoice{}).
		Where("id IN (?)", subQuery.Select("invoice_payment_submission.invoice_id")).
		Update("has_submission", false).Error
	if err != nil {
		return err
	}

	return nil
}

func (p *paymentSubmissionMutationImpl) UpdateStatus(status db_models.PaymentSubmissionStatus) error {
	if p.data == nil {
		err := errors.New("data payment submission not initialized")
		return err
	}

	switch p.data.Status {
	case db_models.PaymentSubmissionStatusAccepted, db_models.PaymentSubmissionStatusRejected:
		err := fmt.Errorf("payment submission have been %s", p.data.Status)
		return err
	}

	err := p.tx.Model(&db_models.PaymentSubmission{}).
		Where("id = ?", p.data.ID).
		Updates(map[string]interface{}{
			"status":          status,
			"completed_by_id": p.agent.GetUserID(),
		}).Error
	if err != nil {
		return err
	}

	err = p.CreateLog(p.data.ID, status)
	if err != nil {
		return err
	}

	return nil
}

func (p *paymentSubmissionMutationImpl) CreateAdjustment(data ...*db_models.Invoice) error {
	for _, item := range data {
		adjustmentInvoice := db_models.Invoice{
			OrderID:    item.OrderID,
			FromTeamID: item.ToTeamID,
			ToTeamID:   item.FromTeamID,
			Status:     db_models.InvoiceNotPaid,
			Amount:     item.Amount,
			Created:    time.Now(),
			Type:       item.GetAdjustmentType(),
		}
		err := p.tx.Save(&adjustmentInvoice).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *paymentSubmissionMutationImpl) CreateLog(subID uint, status db_models.PaymentSubmissionStatus) error {
	logdata := &db_models.PaymentSubmissionLog{
		Status:              status,
		From:                p.agent.GetAgentType(),
		ByUserID:            p.agent.GetUserID(),
		PaymentSubmissionID: subID,
		CreatedAt:           time.Now(),
	}

	err := p.tx.Save(logdata).Error
	if err != nil {
		return err
	}

	return nil
}
