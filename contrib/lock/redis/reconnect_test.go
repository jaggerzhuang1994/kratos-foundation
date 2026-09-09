package redis

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

func TestLockWaitsThroughTransientFailuresButTryLockDoesNot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{lockScriptObtain: {{err: io.EOF}, {err: io.EOF}, {err: io.EOF}, {value: "OK"}}})
		locker := newWithClient(client, defaultOptions())
		started := time.Now()
		lease, err := locker.Lock(context.Background(), "job", time.Second)
		if err != nil || lease == nil {
			t.Fatalf("Lock=%v %v; want recovery before starting task", lease, err)
		}
		if elapsed := time.Since(started); elapsed < 560*time.Millisecond || elapsed > 700*time.Millisecond {
			t.Fatalf("retry waits=%s", elapsed)
		}
		client = newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{lockScriptObtain: {{err: io.EOF}, {value: "OK"}}})
		lease, err = newWithClient(client, defaultOptions()).TryLock(context.Background(), "job", time.Second)
		if lease != nil || !errors.Is(err, io.EOF) || len(client.callsFor(lockScriptObtain)) != 1 {
			t.Fatalf("TryLock=%v %v", lease, err)
		}
	})
}

func TestLockTransientWaitCanBeCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{lockScriptObtain: {{err: io.EOF}}})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := newWithClient(client, defaultOptions()).Lock(ctx, "job", time.Second)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Lock=%v; want canceled retry", err)
		}
	})
}
