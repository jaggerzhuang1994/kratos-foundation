package kafka

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultMaxAttempts = 3
	defaultMinBackoff  = 500 * time.Millisecond
	defaultMaxBackoff  = 30 * time.Second
	// maxRetryAttempts 保留足够的长时重试窗口，同时拒绝误配为事实上永不进入死信的循环。
	maxRetryAttempts = 1000
)

// RetryPolicy 控制消息失败或进入死信队列前的进程内重试。
// MaxAttempts 包含第一次 Handler 调用；传 nil 时使用组件默认策略，传非 nil 时零 Backoff 明确表示不等待。
type RetryPolicy struct {
	MaxAttempts int
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
}

type retryPolicy struct {
	MaxAttempts int
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
}

// resolveRetryPolicy 将可选策略展开为完整值，同时拦截无限重试或倒置退避区间。
func resolveRetryPolicy(value *RetryPolicy) (retryPolicy, error) {
	policy := retryPolicy{
		MaxAttempts: defaultMaxAttempts,
		MinBackoff:  defaultMinBackoff,
		MaxBackoff:  defaultMaxBackoff,
	}
	if value == nil {
		return policy, nil
	}
	policy.MaxAttempts = value.MaxAttempts
	policy.MinBackoff = value.MinBackoff
	policy.MaxBackoff = value.MaxBackoff
	if policy.MaxAttempts <= 0 {
		return retryPolicy{}, fmt.Errorf("retry max attempts must be positive")
	}
	if policy.MaxAttempts > maxRetryAttempts {
		return retryPolicy{}, fmt.Errorf(
			"retry max attempts exceeds %d",
			maxRetryAttempts,
		)
	}
	if policy.MinBackoff < 0 || policy.MaxBackoff < 0 {
		return retryPolicy{}, fmt.Errorf("retry backoff cannot be negative")
	}
	if policy.MinBackoff > 0 && policy.MaxBackoff > 0 && policy.MaxBackoff < policy.MinBackoff {
		return retryPolicy{}, fmt.Errorf("retry max backoff cannot be less than min backoff")
	}
	return policy, nil
}

// waitBackoff 等待可取消的重试间隔，避免关停被 sleep 阻塞。
func waitBackoff(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// nextBackoff 计算不溢出且不超过上限的倍增退避时间。
func nextBackoff(current, maximum time.Duration) time.Duration {
	if current <= 0 || maximum <= 0 {
		return 0
	}
	if current >= maximum {
		return maximum
	}
	if current > maximum/2 {
		return maximum
	}
	return min(current*2, maximum)
}
