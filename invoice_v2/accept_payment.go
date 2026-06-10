package invoice_v2

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
)

// AcceptPayment implements [invoice_ifaceconnect.InvoiceServiceHandler].
//
// The receiver confirms a pending payment: it settles the payer's PAYABLE (a
// double entry of type PAYMENT), clears the in-flight PendingPaymentAmount on
// both sides, and marks the payment ACCEPTED.
func (s *invoiceServiceImpl) AcceptPayment(
	ctx context.Context,
	req *connect.Request[invoice_iface.AcceptPaymentRequest],
) (*connect.Response[invoice_iface.AcceptPaymentResponse], error) {
	pay := req.Msg

	caller, err := access_interceptors.GetIdentityFromCtx(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	completedBy := uint64(caller.IdentityId)

	now := time.Now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := loadPendingPayment(tx, pay.PaymentId, pay.TeamId, pay.ForTeamId)
		if err != nil {
			return err
		}

		// Settle the debt: reduce the payer's PAYABLE (and mirror the receivable).
		note := fmt.Sprintf("payment #%d", p.ID)
		if err := postDoubleEntry(tx, p.TeamID, p.ForTeamID, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE, invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PAYMENT, p.Amount, note, completedBy, now); err != nil {
			return err
		}

		// Clear the in-flight amount on both sides.
		if err := adjustPending(tx, p.TeamID, p.ForTeamID, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE, -p.Amount, now); err != nil {
			return err
		}
		if err := adjustPending(tx, p.ForTeamID, p.TeamID, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE, -p.Amount, now); err != nil {
			return err
		}

		return tx.Model(&invoice_models.InvoicePayment{}).
			Where("id = ?", p.ID).
			Updates(map[string]interface{}{
				"status":          invoice_iface.PaymentStatus_PAYMENT_STATUS_ACCEPTED,
				"accepted_at":     now,
				"completed_by_id": completedBy,
				"updated_at":      now,
			}).Error
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.AcceptPaymentResponse{}), nil
}
