package invoice_v2

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
)

// CreatePayment implements [invoice_ifaceconnect.InvoiceServiceHandler].
//
// It records a PENDING payment from team_id to for_team_id and bumps the
// PendingPaymentAmount on both the payer's PAYABLE and the receiver's RECEIVABLE
// balances. The actual balance settlement happens on AcceptPayment.
func (s *invoiceServiceImpl) CreatePayment(
	ctx context.Context,
	req *connect.Request[invoice_iface.CreatePaymentRequest],
) (*connect.Response[invoice_iface.CreatePaymentResponse], error) {
	pay := req.Msg

	if pay.TeamId == pay.ForTeamId {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("team_id and for_team_id must differ"))
	}

	caller, err := access_interceptors.GetIdentityFromCtx(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	now := time.Now()
	payment := invoice_models.Payment{
		TeamID:      pay.TeamId,
		ForTeamID:   pay.ForTeamId,
		Amount:      pay.Amount,
		Note:        pay.Note,
		DocumentID:  pay.DocumentId,
		Status:      invoice_iface.PaymentStatus_PAYMENT_STATUS_PENDING,
		CreatedByID: uint64(caller.IdentityId),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}
		// Track the in-flight amount on both sides of the pair.
		if err := adjustPending(tx, pay.TeamId, pay.ForTeamId, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE, pay.Amount, now); err != nil {
			return err
		}
		return adjustPending(tx, pay.ForTeamId, pay.TeamId, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE, pay.Amount, now)
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.CreatePaymentResponse{Id: payment.ID}), nil
}
