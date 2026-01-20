package server

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app"
)

var ProviderSet = wire.NewSet(
	NewDefaultConfig,
	NewConfig,
	NewMiddlewares,
	NewHttpServerOptions,
	NewHttpServer,
	NewGrpcServerOptions,
	NewGrpcServer,
	NewWebsocketServer,
	NewRegister,
	wire.Bind(new(app.ServerProvider), new(Register)),
)
