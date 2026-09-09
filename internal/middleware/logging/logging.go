package logging

import (
	"context"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是访问日志中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Logging

// Server 创建服务端访问日志中间件，并让 WebSocket 请求自行管理长连接日志。
func Server(log log.Logger, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	m := logging.Server(withDeadlineFields(log))

	return func(handler middleware.Handler) middleware.Handler {
		logHandler := m(handler)
		return func(ctx context.Context, req any) (any, error) {
			// WebSocket 生命周期远长于一次 RPC，在握手层记录耗时会产生误导性指标。
			request, ok := http.RequestFromServerContext(ctx)
			if !ok || !websocket.IsWebSocketUpgrade(request) {
				return logHandler(ctx, req)
			}
			return handler(ctx, req)
		}
	}
}

// Client 创建包含截止时间字段的客户端访问日志中间件。
func Client(log log.Logger, config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}
	return logging.Client(withDeadlineFields(log))
}

// withDeadlineFields 为访问日志补充实际预算来源与关键时长。
func withDeadlineFields(logger log.Logger) log.Logger {
	return logger.With(deadlineFields()...)
}

func deadlineFields() []any {
	return []any{
		"deadline.source", kratoslog.Valuer(func(ctx context.Context) any {
			info, ok := deadline.InfoFromContext(ctx)
			if !ok {
				return nil
			}
			return info.Source
		}),
		"deadline.remaining_ms", kratoslog.Valuer(func(ctx context.Context) any {
			effective, ok := ctx.Deadline()
			if !ok {
				return nil
			}
			return time.Until(effective).Milliseconds()
		}),
		"deadline.fallback_ms", deadlineDuration(func(info deadline.Info) time.Duration {
			return info.FallbackTimeout
		}),
		"deadline.max_ms", deadlineDuration(func(info deadline.Info) time.Duration {
			return info.MaxTimeout
		}),
		"deadline.min_budget_ms", deadlineDuration(func(info deadline.Info) time.Duration {
			return info.MinBudget
		}),
	}
}

func deadlineDuration(value func(deadline.Info) time.Duration) kratoslog.Valuer {
	return func(ctx context.Context) any {
		info, ok := deadline.InfoFromContext(ctx)
		if !ok {
			return nil
		}
		return value(info).Milliseconds()
	}
}
