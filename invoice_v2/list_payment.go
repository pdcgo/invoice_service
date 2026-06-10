package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/shared/db_connect"
	"gorm.io/gorm"
)

// ListPayment implements [invoice_ifaceconnect.InvoiceServiceHandler]. It lists
// the OUTGOING payments of the scoped team (team_id = payer), optionally filtered
// by counterparty and status.
func (s *invoiceServiceImpl) ListPayment(
	ctx context.Context,
	req *connect.Request[invoice_iface.ListPaymentRequest],
) (*connect.Response[invoice_iface.ListPaymentResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &invoice_iface.ListPaymentResponse{
		Payments: []*invoice_iface.Payment{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []*invoice_models.InvoicePayment
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		query := db.
			Model(&invoice_models.InvoicePayment{}).
			Scopes(func(d *gorm.DB) *gorm.DB {
				d = d.Where("team_id = ?", pay.TeamId)
				if pay.ForTeamId > 0 {
					d = d.Where("for_team_id = ?", pay.ForTeamId)
				}
				if pay.Status != invoice_iface.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED {
					d = d.Where("status = ?", pay.Status)
				}
				return d
			})
		return query, nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	if err := paginated.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, row := range rows {
		if row == nil {
			continue
		}
		result.Payments = append(result.Payments, toProtoPayment(row))
	}

	return connect.NewResponse(result), nil
}
