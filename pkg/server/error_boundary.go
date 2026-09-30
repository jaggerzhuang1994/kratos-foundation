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
)

// normalizeErrors 统一传输错误，在访问日志关闭时仍保留一次服务端故障诊断。
func normalizeErrors(logger log.Logger) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			reply, err := next(ctx, req)
			err = foundationerrors.Normalize(err)
			if err != nil && foundationerrors.Code(err) >= 500 {
				summary := error(err)
				if cause := errors.Unwrap(err); cause != nil {
					summary = cause
				}
				requestLogger(logger, ctx).With(
					"event", "server.request.failed",
					"code", foundationerrors.Code(err),
					"reason", foundationerrors.Reason(err),
					"error", summary,
					"error.detail", log.DebugOnly(kratoslog.Valuer(func(context.Context) any {
						return fmt.Sprintf("%+v", err)
					})),
				).Error("request failed with a server error")
			}
			return reply, err
		}
	}
}

// recoverRequests 防止请求 panic 终止服务，不记录可能包含认证材料的请求和 panic 原文。
// panic 会越过内层错误边界，因此只在此处记录类型，并按请求 debug 展开堆栈。
func recoverRequests(logger log.Logger) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					stack := string(debug.Stack())
					requestLogger(logger, ctx).With(
						"event", "server.request.panic.recovered",
						"panic_type", fmt.Sprintf("%T", recovered),
						"stack", log.DebugOnly(stack),
					).Error("recovered from a panic while handling a request")
					reply = nil
					err = foundationerrors.New(500, "UNKNOWN", "Internal Server Error").WithMetadata(map[string]string{"err_stack": stack})
				}
			}()
			return next(ctx, req)
		}
	}
}

// requestLogger 只使用传输契约中的协议、端点和操作名，不读取请求参数或正文。
func requestLogger(logger log.Logger, ctx context.Context) log.Logger {
	logger = logger.WithContext(ctx)
	if info, ok := transport.FromServerContext(ctx); ok {
		return logger.With("transport", info.Kind().String(), "endpoint", info.Endpoint(), "operation", info.Operation())
	}
	return logger
}
