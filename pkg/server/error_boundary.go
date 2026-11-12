package server

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	stdgrpc "google.golang.org/grpc"
)

// normalizeErrors 统一传输错误，在访问日志关闭时仍保留一次服务端故障诊断。
func normalizeErrors(logger log.Logger) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			reply, err := next(ctx, req)
			err = foundationerrors.Normalize(err)
			// 内层恢复的 panic 已记录过类型、panic 值与堆栈，这里不再重复记录。
			if err != nil && foundationerrors.Code(err) >= 500 && !errors.Is(err, errRequestPanicked) {
				summary := err
				if cause := errors.Unwrap(err); cause != nil {
					summary = cause
				}
				requestLogger(logger, ctx).With(
					"event", "server.request.failed",
					"code", foundationerrors.Code(err),
					"reason", foundationerrors.Reason(err),
					"error", summary,
					// 完整错误链与堆栈随故障日志输出，不依赖 Debug 级别：线上默认 Info 也要能直接定位。
					"error.detail", kratoslog.Valuer(func(context.Context) any {
						return fmt.Sprintf("%+v", err)
					}),
				).Error("request failed")
			}
			return reply, err
		}
	}
}

// errRequestPanicked 标记由 panic 恢复出的 500，供外层错误边界识别，避免同一次 panic 记录两条故障日志。
var errRequestPanicked = errors.New("request panicked")

// recoverRequests 防止请求 panic 终止服务，记录 panic 类型、panic 值与堆栈，不记录请求参数或正文。
// panic 会越过内层错误边界，只能在此处记录；堆栈不依赖 Debug 级别，线上默认 Info 也要能直接定位。
// 链上登记两层：最外层兜住所有中间件的 panic；访问日志之内的一层让业务 panic 的日志带上 trace.id，
// 并让恢复出的 500 经过访问日志与请求指标。
func recoverRequests(logger log.Logger) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					stack := string(debug.Stack())
					requestLogger(logger, ctx).With(
						"event", "server.request.panic.recovered",
						"panic_type", fmt.Sprintf("%T", recovered),
						"panic", fmt.Sprint(recovered),
						"stack", stack,
					).Error("request panic recovered")
					reply = nil
					err = foundationerrors.New(500, "UNKNOWN", "Internal Server Error").WithCause(errRequestPanicked).WithMetadata(map[string]string{"err_stack": stack})
				}
			}()
			return next(ctx, req)
		}
	}
}

// recoverStreams 恢复 gRPC 流式处理器的 panic。Kratos 的流拦截器不把中间件链套在流处理器外，
// recoverRequests 覆盖不到流式接口；grpc-go 也不恢复处理器 panic，未恢复时整个进程退出。
func recoverStreams(logger log.Logger) stdgrpc.StreamServerInterceptor {
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				stack := string(debug.Stack())
				logger.WithContext(stream.Context()).With(
					"event", "server.request.panic.recovered",
					"transport", "grpc",
					"operation", info.FullMethod,
					"panic_type", fmt.Sprintf("%T", recovered),
					"panic", fmt.Sprint(recovered),
					"stack", stack,
				).Error("request panic recovered")
				err = foundationerrors.New(500, "UNKNOWN", "Internal Server Error").WithMetadata(map[string]string{"err_stack": stack})
			}
		}()
		return handler(srv, stream)
	}
}

// requestLogger 只使用传输契约中的协议、端点和操作名，不读取请求参数或正文。
func requestLogger(logger log.Logger, ctx context.Context) log.Logger {
	logger = logger.WithContext(ctx)
	if info, ok := transport.FromServerContext(ctx); ok {
		return logger.With("transport", info.Kind().String(), "operation", info.Operation())
	}
	return logger
}
