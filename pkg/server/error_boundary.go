package server

import (
	"context"
	"fmt"
	"runtime/debug"

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
				logger.WithContext(ctx).Errorw(
					"msg", "normalizeErrors | request.failed", "operation", requestOperation(ctx),
					"code", foundationerrors.Code(err), "reason", foundationerrors.Reason(err),
					"error", fmt.Sprintf("%+v", err),
				)
			}
			return reply, err
		}
	}
}

// recoverRequests 防止请求 panic 终止服务，不记录可能包含认证材料的请求和 panic 原文。
// panic 会越过内层错误边界，因此只在此处记录类型与堆栈。
func recoverRequests(logger log.Logger) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					stack := string(debug.Stack())
					logger.WithContext(ctx).Errorw(
						"msg", "recoverRequests | request.panic", "operation", requestOperation(ctx),
						"panic_type", fmt.Sprintf("%T", recovered), "stack", stack,
					)
					reply = nil
					err = foundationerrors.New(500, "UNKNOWN", "Internal Server Error").WithMetadata(map[string]string{"err_stack": stack})
				}
			}()
			return next(ctx, req)
		}
	}
}

func requestOperation(ctx context.Context) string {
	if info, ok := transport.FromServerContext(ctx); ok {
		return info.Operation()
	}
	return ""
}
