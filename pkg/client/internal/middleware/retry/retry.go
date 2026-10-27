// Package retry 提供 client 私有的失败重试中间件。
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是重试中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Retry

const (
	defaultMaxAttempts = 3
	defaultBackoff     = 100 * time.Millisecond
	defaultMaxBackoff  = time.Second
	// circuitBreakerReason 是 Kratos 熔断打开时返回的原因；熔断期间重试只会再次被拒绝。
	circuitBreakerReason = "CIRCUITBREAKER"
)

// Validate 在客户端构造和热更新入口检查重试参数。
func Validate(config Config) error {
	if !config.GetEnable() {
		return nil
	}
	backoff, maxBackoff := durations(config)
	if backoff > maxBackoff {
		return fmt.Errorf("retry backoff %s exceeds max_backoff %s", backoff, maxBackoff)
	}
	for _, operation := range config.GetOperations() {
		if operation == "" {
			return errors.New("retry operations must not be empty strings")
		}
	}
	return nil
}

func durations(config Config) (time.Duration, time.Duration) {
	backoff, maxBackoff := defaultBackoff, defaultMaxBackoff
	if config.Backoff != nil {
		backoff = config.GetBackoff().AsDuration()
	}
	if config.MaxBackoff != nil {
		maxBackoff = config.GetMaxBackoff().AsDuration()
	}
	return backoff, maxBackoff
}

// Client 根据配置创建重试中间件；未启用时返回 nil 便于组装层跳过。
// 每次尝试都经过其后的指标、日志与熔断中间件，全部尝试共享调用方的截止时间。
func Client(config Config) middleware.Middleware {
	if !config.GetEnable() {
		return nil
	}
	attempts := defaultMaxAttempts
	if config.MaxAttempts != nil {
		attempts = int(config.GetMaxAttempts())
	}
	backoff, maxBackoff := durations(config)
	operations := slices.Clone(config.GetOperations())
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if !retryable(ctx, operations) {
				return next(ctx, req)
			}
			wait := backoff
			for attempt := 1; ; attempt++ {
				reply, err := next(ctx, req)
				if err == nil || attempt >= attempts || !transient(err) {
					return reply, err
				}
				// 抖动取 [wait/2, wait)，避免多个调用方同时重试。
				delay := wait/2 + rand.N(wait/2+1)
				if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay {
					return reply, err
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return reply, err
				case <-timer.C:
				}
				wait = min(wait*2, maxBackoff)
			}
		}
	}
}

// retryable 判断本次调用能否安全重放：HTTP 只允许无请求体的方法，gRPC 只允许显式列出的方法。
func retryable(ctx context.Context, operations []string) bool {
	tr, ok := transport.FromClientContext(ctx)
	if !ok {
		return false
	}
	if httpTransport, ok := tr.(interface{ Request() *http.Request }); ok && tr.Kind() == transport.KindHTTP {
		request := httpTransport.Request()
		if request == nil {
			return false
		}
		switch request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return true
		}
		return false
	}
	return tr.Kind() == transport.KindGRPC && slices.Contains(operations, tr.Operation())
}

// transient 只把服务不可用视为可重试；熔断打开、超时与业务错误不重试。
func transient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	status := kratoserrors.FromError(err)
	return status.Code == http.StatusServiceUnavailable && status.Reason != circuitBreakerReason
}
