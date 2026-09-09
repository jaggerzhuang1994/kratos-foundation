package job

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type delayedRefreshLease struct {
	recordingLease
	started   chan struct{}
	finish    chan struct{}
	unlocks   atomic.Int32
	startOnce sync.Once
}

func (l *delayedRefreshLease) Refresh(context.Context, time.Duration) error {
	l.startOnce.Do(func() { close(l.started) })
	<-l.finish
	return nil
}
func (l *delayedRefreshLease) Unlock(context.Context) error { l.unlocks.Add(1); return nil }

func TestRefreshTimeoutCancelsExecutionEvenWhenLeaseIgnoresContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lease := &delayedRefreshLease{started: make(chan struct{}), finish: make(chan struct{})}
		guard := newLockExecutionGuard(context.Background(), "job", lease, &lockCoordinator{leaseTTL: time.Second, refreshInterval: 100 * time.Millisecond, operationTimeout: 50 * time.Millisecond})
		defer func() {
			close(lease.finish)
			if err := guard.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		}()
		<-lease.started
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if !errors.Is(context.Cause(guard.Context()), ErrCoordinationLost) {
			t.Errorf("execution cause=%v; timed-out refresh must cancel before returning", context.Cause(guard.Context()))
		}
	})
}

func TestReleaseBoundsWaitForUncooperativeRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lease := &delayedRefreshLease{started: make(chan struct{}), finish: make(chan struct{})}
		guard := newLockExecutionGuard(context.Background(), "job", lease, &lockCoordinator{leaseTTL: time.Second, refreshInterval: 100 * time.Millisecond, operationTimeout: 50 * time.Millisecond})
		<-lease.started
		released := make(chan error, 1)
		go func() { released <- guard.Release() }()
		select {
		case err := <-released:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Release=%v; want bounded cleanup error", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Release waited indefinitely for a refresh ignoring cancellation")
		}
		if lease.unlocks.Load() != 0 {
			t.Error("Unlock raced with the in-flight refresh")
		}
		if guard.Context().Err() == nil {
			t.Error("Release left old execution active")
		}
		close(lease.finish)
		synctest.Wait()
		if lease.unlocks.Load() != 0 {
			t.Error("timed-out cleanup must leave lease to expire, not unlock concurrently")
		}
	})
}

func TestSuccessfulRefreshDoesNotLeaveTimeoutCallbacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := newLockExecutionGuard(context.Background(), "job", &recordingLease{}, &lockCoordinator{leaseTTL: time.Second, refreshInterval: 100 * time.Millisecond, operationTimeout: 50 * time.Millisecond})
		defer func() {
			if err := guard.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		}()
		time.Sleep(350 * time.Millisecond)
		synctest.Wait()
		if guard.Context().Err() != nil {
			t.Fatalf("successful refresh canceled execution: %v", context.Cause(guard.Context()))
		}
	})
}
