//go:build wireinject
// +build wireinject

package main

import (
	"net/http"

	"github.com/google/wire"
	"github.com/pdcgo/invoice_service"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/urfave/cli/v3"
)

func InitializeApp() (*cli.Command, error) {
	wire.Build(
		configs.NewProductionConfig,
		http.NewServeMux,
		custom_connect.NewRegisterReflect,
		custom_connect.NewDefaultInterceptor,
		NewDatabase,
		NewRedisDatabase,
		NewCacheManager,
		invoice_service.NewInvoicePushHandler,
		invoice_service.NewInvoicePushHttpHandler,
		invoice_service.NewRegister,
		NewServiceApiFunc,
		NewApp,
	)

	return &cli.Command{}, nil
}
