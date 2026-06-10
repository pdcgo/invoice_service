package invoice_service

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_v2"
	"github.com/pdcgo/san_collection/san_caches"
	"github.com/pdcgo/schema/services/invoice_iface/v2/invoice_ifaceconnect"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
)

type ServiceReflectNames []string
type RegisterHandler func() ServiceReflectNames

// NewRegister mounts the v2 Connect InvoiceService onto mux and returns the
// gRPC-reflection service names. Only the v2 service is registered here; the
// legacy grpc-gateway service is intentionally left out. The access interceptor
// enforces each request's (role_base.v1.request_policy) and injects the caller
// identity into context. It also mounts the Pub/Sub push endpoint.
func NewRegister(
	mux *http.ServeMux,
	db *gorm.DB,
	cfg *configs.AppConfig,
	defaultInterceptor custom_connect.DefaultInterceptor,
	cacheMgr san_caches.CacheManager,
	invoicePushHttpHandler InvoicePushHttpHandler,
) RegisterHandler {
	return func() ServiceReflectNames {
		grpcReflects := ServiceReflectNames{}

		roleOpt := connect.WithInterceptors(access_interceptors.NewAccessInterceptor(db, cfg.JwtSecret, cacheMgr))
		path, handler := invoice_ifaceconnect.NewInvoiceServiceHandler(
			invoice_v2.NewInvoiceService(db),
			defaultInterceptor,
			roleOpt,
		)
		mux.Handle(path, handler)
		grpcReflects = append(grpcReflects, invoice_ifaceconnect.InvoiceServiceName)

		mux.HandleFunc("/invoice/push", invoicePushHttpHandler)

		return grpcReflects
	}
}
