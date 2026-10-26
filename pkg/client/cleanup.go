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

// endActivity 与关闭状态共用 mu，保证停机后不会再增加活动计数。
func (f *factory) endActivity() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activities--
	f.signalDrainedLocked()
}

// signalDrainedLocked 必须持有 mu 调用；关闭期间构建、配置更新和租约全部结束时唤醒 cleanup。
// 直接通知等待者，不为可能永不归还的租约启动等待协程。
func (f *factory) signalDrainedLocked() {
	if f.closed && f.activities == 0 && len(f.draining) == 0 && !f.drainedClosed {
		f.drainedClosed = true
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
		f.publishLocked()
		for _, slot := range f.slots {
			if slot.build != nil && slot.build.cancel != nil {
				cancels = append(cancels, slot.build.cancel)
			}
			if client, idle := f.retireLocked(slot.name, slot.current, retireFactoryClosed); idle {
				retired = append(retired, client)
			}
		}
		f.signalDrainedLocked()
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
		// 同时收回当前及热更新退役版本，防止仍被租用的连接泄漏；晚到的 release 不会重复关闭。
		f.mu.Lock()
		pending := f.activities + len(f.draining)
		retired = nil
		for version, name := range f.draining {
			retired = append(retired, detachRetired(name, version))
		}
		clear(f.draining)
		f.mu.Unlock()
		if pending == 0 {
			return
		}
		f.logger.With("event", "client.cleanup.timeout", "timeout", f.cleanupTimeout, "pending", pending, "connections", len(retired)).Warn("client drain timed out; closing connections")
		for _, client := range retired {
			f.closeAndLog(client)
		}
	})
}
