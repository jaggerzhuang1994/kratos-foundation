package logging

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

type Config = *config_pb.Middleware_Logging

func Server(log log.Log, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	m := logging.Server(log)

	return func(handler middleware.Handler) middleware.Handler {
		logHandler := m(handler)
		return func(ctx context.Context, req any) (any, error) {
			// 如果是 ws 请求 则不记录log，在 ws 内部自己记录log
			request, ok := http.RequestFromServerContext(ctx)
			if !ok || !websocket.IsWebSocketUpgrade(request) {
				return logHandler(ctx, req)
			}
			return handler(ctx, req)
		}
	}
}

func Client(log log.Log, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}
	return logging.Client(log)
}
