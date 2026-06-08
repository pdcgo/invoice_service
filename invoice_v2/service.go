package invoice_v2

import (
	"github.com/pdcgo/schema/services/invoice_iface/v2/invoice_ifaceconnect"
	"gorm.io/gorm"
)

// invoiceServiceImpl is the v2 Connect-RPC implementation of
// [invoice_ifaceconnect.InvoiceServiceHandler]. Handlers live one-per-file and
// currently return CodeUnimplemented; fill them in as the features land.
type invoiceServiceImpl struct {
	db *gorm.DB
}

func NewInvoiceService(db *gorm.DB) *invoiceServiceImpl {
	return &invoiceServiceImpl{db: db}
}

// Compile-time assertion that the skeleton satisfies the generated handler.
var _ invoice_ifaceconnect.InvoiceServiceHandler = (*invoiceServiceImpl)(nil)
