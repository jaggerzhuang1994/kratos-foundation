// Package reconnect 提供建连和消费恢复共用的退避计算，不管理资源或重放业务操作。
package reconnect

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"syscall"
	"time"
)

// Backoff 是无状态指数退避；次数由各连接或消费循环独立维护，无须共享锁。
// 零值从 100ms 开始，翻倍到 5s 上限，实际等待为该间隔的 80%–100%。
type Backoff struct {
	Min time.Duration
	Max time.Duration
}

// Delay 返回第 attempt 次等待间隔；首次失败后传 0，成功后调用方重置次数。
func (b Backoff) Delay(attempt int) time.Duration {
	minimum, maximum := b.Min, b.Max
	if minimum <= 0 {
		minimum = 100 * time.Millisecond
	}
	if maximum <= 0 {
		maximum = 5 * time.Second
	}
	delay := min(minimum, maximum)
	// 先检查上限，既避免 Duration 溢出，也避免超大次数导致长循环。
	for attempt > 0 && delay < maximum {
		if delay > maximum/2 {
			delay = maximum
		} else {
			delay *= 2
		}
		attempt--
	}
	if jitter := delay / 5; jitter > 0 {
		delay -= time.Duration(rand.Int64N(int64(jitter) + 1))
	}
	return delay
}

// Wait 等待退避间隔，取消或超时立即结束等待。
func (b Backoff) Wait(ctx context.Context, attempt int) error {
	timer := time.NewTimer(b.Delay(attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Transient 判断可恢复的网络错误。调用方还须检查自己的 ctx，且只用于基础设施操作。
// 业务处理函数即使返回相同错误，也不能据此重放；协议认证等错误由各适配器判定。
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return false
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) {
		return false
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) && dnsError.IsNotFound {
		return false
	}
	var addressError *net.AddrError
	var networkError net.UnknownNetworkError
	if errors.As(err, &addressError) || errors.As(err, &networkError) {
		return false
	}
	for _, cause := range []error{
		io.EOF, io.ErrUnexpectedEOF, syscall.ECONNREFUSED, syscall.ECONNRESET,
		syscall.ECONNABORTED, syscall.EPIPE, syscall.ENETUNREACH, syscall.EHOSTUNREACH,
	} {
		if errors.Is(err, cause) {
			return true
		}
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return true
	}
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}
