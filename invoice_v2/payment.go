package invoice_v2

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// loadPendingPayment locks the payment row, verifies it belongs to the
// (teamID, forTeamID) pair, and requires it to be PENDING. Used by accept/reject.
func loadPendingPayment(tx *gorm.DB, paymentID, teamID, forTeamID uint64) (*invoice_models.InvoicePayment, error) {
	var p invoice_models.InvoicePayment
	err := lockForUpdate(tx).Where("id = ?", paymentID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("payment not found"))
		}
		return nil, err
	}
	if p.TeamID != teamID || p.ForTeamID != forTeamID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("payment does not match team_id/for_team_id"))
	}
	if p.Status != invoice_iface.PaymentStatus_PAYMENT_STATUS_PENDING {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("payment already %s", p.Status))
	}
	return &p, nil
}

// toProtoPayment maps a stored Payment to its proto representation.
func toProtoPayment(p *invoice_models.InvoicePayment) *invoice_iface.Payment {
	out := &invoice_iface.Payment{
		Id:          p.ID,
		TeamId:      p.TeamID,
		ForTeamId:   p.ForTeamID,
		Amount:      p.Amount,
		Note:        p.Note,
		DocumentId:  p.DocumentID,
		Status:      p.Status,
		CreatedById: p.CreatedByID,
		CreatedAt:   timestamppb.New(p.CreatedAt),
	}
	if p.CompletedByID != nil {
		out.CompletedById = *p.CompletedByID
	}
	if p.AcceptedAt != nil {
		out.AcceptedAt = timestamppb.New(*p.AcceptedAt)
	}
	if p.RejectedAt != nil {
		out.RejectedAt = timestamppb.New(*p.RejectedAt)
	}
	return out
}
