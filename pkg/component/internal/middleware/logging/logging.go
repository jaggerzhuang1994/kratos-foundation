package logging

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Logging

func Enable(config *Config) bool {
	return !config.GetDisable()
}

func Server(log *log.Log, _ *Config) middleware.Middleware {
	m := logging.Server(log.GetLogger())

	return func(handler middleware.Handler) middleware.Handler {
		innerHandler := m(handler)
		return func(ctx context.Context, req any) (any, error) {
			// 如果是 ws 请求 则不记录log，在 ws 内部自己记录log
			request, ok := http.RequestFromServerContext(ctx)
			if !ok || !websocket.IsWebSocketUpgrade(request) {
				return innerHandler(ctx, req)
			}
			return handler(ctx, req)
		}
	}
}

func Client(log *log.Log, _ *Config) middleware.Middleware {
	return logging.Client(log.GetLogger())
}
