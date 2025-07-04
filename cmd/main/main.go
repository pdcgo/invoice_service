package main

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/shared/interfaces/invoice_iface"
	"github.com/pdcgo/shared/pkg/ware_cache"
)

func main() {
	ctx := context.Background()
	mux := runtime.NewServeMux()

	service := invoice_service.NewInvoiceService(nil, ware_cache.NewLocalCache())
	invoice_iface.RegisterInvoiceServiceHandlerServer(ctx, mux, service)
	// pb.RegisterGreeterHandlerServer(ctx, mux, &server{}) // No gRPC server needed!

	http.ListenAndServe("localhost:8080", mux) // App Engine uses 8080
}
