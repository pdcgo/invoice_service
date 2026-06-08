package invoice_v2

import (
	"context"
	"time"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
)

// RejectPayment implements [invoice_ifaceconnect.InvoiceServiceHandler].
//
// The receiver declines a pending payment: it clears the in-flight
// PendingPaymentAmount on both sides (no balance settlement) and marks the
// payment REJECTED.
func (s *invoiceServiceImpl) RejectPayment(
	ctx context.Context,
	req *connect.Request[invoice_iface.RejectPaymentRequest],
) (*connect.Response[invoice_iface.RejectPaymentResponse], error) {
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

		// Release the in-flight amount; balances are untouched.
		if err := adjustPending(tx, p.TeamID, p.ForTeamID, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE, -p.Amount, now); err != nil {
			return err
		}
		if err := adjustPending(tx, p.ForTeamID, p.TeamID, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE, -p.Amount, now); err != nil {
			return err
		}

		return tx.Model(&invoice_models.Payment{}).
			Where("id = ?", p.ID).
			Updates(map[string]interface{}{
				"status":          invoice_iface.PaymentStatus_PAYMENT_STATUS_REJECTED,
				"rejected_at":     now,
				"completed_by_id": completedBy,
				"updated_at":      now,
			}).Error
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.RejectPaymentResponse{}), nil
}
