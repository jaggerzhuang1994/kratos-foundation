package server

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/websocket"
)

var ProviderSet = wire.NewSet(
	NewConfig,
	NewDefaultMiddleware,
	NewHook,
	NewHttpServer,
	NewGrpcServer,
	websocket.NewServer,
	websocket.NewHook,
	NewRegister,
)
