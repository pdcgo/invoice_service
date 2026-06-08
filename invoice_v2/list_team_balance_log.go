package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
)

// ListTeamBalanceLog implements [invoice_ifaceconnect.InvoiceServiceHandler].
func (s *invoiceServiceImpl) ListTeamBalanceLog(
	ctx context.Context,
	req *connect.Request[invoice_iface.ListTeamBalanceLogRequest],
) (*connect.Response[invoice_iface.ListTeamBalanceLogResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ListTeamBalanceLog is not implemented"))
}
