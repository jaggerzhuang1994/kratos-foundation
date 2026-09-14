package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func clientCleanupTimeout(config *config_pb.Client) (time.Duration, error) {
	value := config.GetCleanupTimeout()
	if value == nil {
		return 30 * time.Second, nil
	}
	if err := value.CheckValid(); err != nil {
		return 0, fmt.Errorf("client cleanup_timeout: %w", err)
	}
	duration := value.AsDuration()
	converted := durationpb.New(duration)
	if duration <= 0 || converted.Seconds != value.Seconds || converted.Nanos != value.Nanos {
		return 0, fmt.Errorf("client cleanup_timeout must be a positive Go duration")
	}
	return duration, nil
}

// endActivity 与租约授予、关闭状态共用 mu，保证停机后不会再增加活动计数。
// 直接通知等待者，不为可能永不归还的租约启动 WaitGroup 等待协程。
func (f *factory) endActivity() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activities--
	if f.closed && f.activities == 0 {
		close(f.drained)
	}
}

func (f *factory) cleanup(cancelSubscription func()) func() {
	return sync.OnceFunc(func() {
		if cancelSubscription != nil {
			cancelSubscription()
		}
		timer := time.NewTimer(f.cleanupTimeout)
		defer timer.Stop()
		var cancels []context.CancelFunc
		var retired []retiredClient
		f.mu.Lock()
		f.closed = true
		f.drained = make(chan struct{})
		if f.activities == 0 {
			close(f.drained)
		}
		for _, slot := range f.slots {
			if slot.build != nil && slot.build.cancel != nil {
				cancels = append(cancels, slot.build.cancel)
			}
			current := slot.current
			current.retired = true
			current.retireReason = retireFactoryClosed
			if current.references == 0 && current.client.present() {
				retired = append(retired, detachRetired(slot.name, current))
			}
		}
		f.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		for _, client := range retired {
			f.closeAndLog(client)
		}
		select {
		case <-f.drained:
			return
		case <-timer.C:
		}
		// 同时收回当前及热更新退役版本，防止旧租约不再可从 slots 找到而泄漏。
		f.mu.Lock()
		pending := f.activities
		retired = nil
		for version, name := range f.leases {
			if version.client.present() {
				version.retired = true
				version.retireReason = retireFactoryClosed
				retired = append(retired, detachRetired(name, version))
			}
		}
		f.mu.Unlock()
		if pending == 0 {
			return
		}
		f.logger.With("function", "factory.cleanup", "timeout", f.cleanupTimeout, "pending", pending, "connections", len(retired)).Warn("Timed out waiting for client leases to be released; force-closing connections")
		for _, client := range retired {
			f.closeAndLog(client)
		}
	})
}
