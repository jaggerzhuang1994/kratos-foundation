package logging

import (
	"context"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/websocket"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
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

	m := accessLog(withDeadlineFields(log), false)

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
	return accessLog(withDeadlineFields(log), true)
}

// withDeadlineFields 为访问日志补充实际预算来源与关键时长。
func withDeadlineFields(logger log.Logger) log.Logger {
	return logger.With(deadlineFields()...)
}

func deadlineFields() []any {
	return []any{
		"deadline.source", log.DebugOnly(kratoslog.Valuer(func(ctx context.Context) any {
			info, ok := deadline.InfoFromContext(ctx)
			if !ok {
				return nil
			}
			return info.Source
		})),
		"deadline.remaining", log.DebugOnly(kratoslog.Valuer(func(ctx context.Context) any {
			effective, ok := ctx.Deadline()
			if !ok {
				return nil
			}
			return time.Until(effective).String()
		})),
		//"deadline.fallback_ms", deadlineDuration(func(info deadline.Info) time.Duration {
		//	return info.FallbackTimeout
		//}),
		//"deadline.max_ms", deadlineDuration(func(info deadline.Info) time.Duration {
		//	return info.MaxTimeout
		//}),
		//"deadline.min_budget_ms", deadlineDuration(func(info deadline.Info) time.Duration {
		//	return info.MinBudget
		//}),
	}
}

// accessLog 只记录请求结果摘要，不读取请求或响应正文，也不重复输出错误堆栈。
// 完整服务端故障由常驻错误边界记录，关闭访问日志不会关闭故障诊断。
func accessLog(logger log.Logger, client bool) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			started := time.Now()
			reply, err := next(ctx, req)
			component := "server"
			info, ok := transport.FromServerContext(ctx)
			if client {
				component = "client"
				info, ok = transport.FromClientContext(ctx)
			}
			var operation, kind string
			if ok {
				operation = info.Operation()
				kind = info.Kind().String()
			}
			logger.WithContext(ctx).With(
				"component", component, // server/client
				"kind", kind, // grpc/http
				"operation", operation, // endpoint
				"code", foundationerrors.Code(foundationerrors.Normalize(err)),
				"reason", foundationerrors.Reason(err),
				"latency", time.Since(started).String(),
			).Info("request completed")
			return reply, err
		}
	}
}
