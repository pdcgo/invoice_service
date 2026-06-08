package invoice_v2

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
)

// ListTeamBalance implements [invoice_ifaceconnect.InvoiceServiceHandler].
func (s *invoiceServiceImpl) ListTeamBalance(
	ctx context.Context,
	req *connect.Request[invoice_iface.ListTeamBalanceRequest],
) (*connect.Response[invoice_iface.ListTeamBalanceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ListTeamBalance is not implemented"))
}
