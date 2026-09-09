package deadline

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	deadlinecore "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	"google.golang.org/grpc/status"
)

// HTTPTimeoutHeader 是 HTTP 请求传播剩余毫秒预算时使用的传输层协议头。
const HTTPTimeoutHeader = "x-request-timeout-ms"

// Server 只负责把 Kratos 和 HTTP 传输信息适配到 deadline 核心。
func Server(store *deadlinecore.Store) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			operation, tr := operationFromServerContext(ctx)
			var cancelIncoming context.CancelFunc
			if tr != nil && tr.Kind() == transport.KindHTTP {
				budget, present, exhausted := incomingHTTPBudget(tr.RequestHeader())
				if exhausted {
					return nil, context.DeadlineExceeded
				}
				if present {
					ctx, cancelIncoming = context.WithTimeout(ctx, budget)
					defer cancelIncoming()
				}
			}
			return invoke(ctx, request, operation, store, handler)
		}
	}
}

// Client 只负责选择路由策略、调用核心派生 Context，并向 HTTP 下游传播预算。
func Client(store *deadlinecore.Store) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			operation, tr := operationFromClientContext(ctx)
			invokeHandler := func(ctx context.Context, request any) (any, error) {
				if tr != nil && tr.Kind() == transport.KindHTTP {
					if remaining, ok := remaining(ctx); ok {
						if remaining <= 0 {
							return nil, clientContextError(tr, context.DeadlineExceeded)
						}
						setHTTPTimeoutHeader(tr.RequestHeader(), remaining)
					}
				}
				response, err := handler(ctx, request)
				if err != nil && ctx.Err() != nil {
					return response, clientContextError(tr, ctx.Err())
				}
				return response, err
			}

			response, err := invoke(ctx, request, operation, store, invokeHandler)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return response, clientContextError(tr, err)
			}
			return response, err
		}
	}
}

// invoke 冻结本次请求选择到的策略，然后把所有 deadline 计算交给核心 Derive。
func invoke(
	ctx context.Context,
	request any,
	operation string,
	store *deadlinecore.Store,
	handler middleware.Handler,
) (any, error) {
	derived, cancel, err := store.Derive(ctx, operation)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return handler(derived, request)
}

func remaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

// clientContextError 把本地上下文错误转换为传输层期望的 gRPC 状态。
func clientContextError(tr transport.Transporter, err error) error {
	if tr != nil && tr.Kind() == transport.KindGRPC {
		return status.FromContextError(err).Err()
	}
	return err
}

func setHTTPTimeoutHeader(header transport.Header, remaining time.Duration) {
	// 毫秒头无法表达小数，向上取整可避免仅因序列化截断而额外缩短下游预算。
	timeoutMillis := remaining.Milliseconds()
	if remaining%time.Millisecond != 0 {
		timeoutMillis++
	}
	header.Set(HTTPTimeoutHeader, strconv.FormatInt(timeoutMillis, 10))
}

// incomingHTTPBudget 解析毫秒预算；非正值表示上游预算已经耗尽。
func incomingHTTPBudget(header transport.Header) (time.Duration, bool, bool) {
	if header == nil {
		return 0, false, false
	}
	value := strings.TrimSpace(header.Get(HTTPTimeoutHeader))
	if value == "" {
		return 0, false, false
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds > math.MaxInt64/int64(time.Millisecond) {
		return 0, false, false
	}
	if milliseconds <= 0 {
		return 0, true, true
	}
	return time.Duration(milliseconds) * time.Millisecond, true, false
}

func operationFromServerContext(ctx context.Context) (string, transport.Transporter) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return "", nil
	}
	return tr.Operation(), tr
}

func operationFromClientContext(ctx context.Context) (string, transport.Transporter) {
	tr, ok := transport.FromClientContext(ctx)
	if !ok {
		return "", nil
	}
	return tr.Operation(), tr
}
