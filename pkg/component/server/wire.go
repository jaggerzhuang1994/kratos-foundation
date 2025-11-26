package server

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewConfig,
	NewHookManager,
	NewHttpServer,
	NewGrpcServer,
	NewManager,
	NewRouteTimeoutMiddleware,
)
