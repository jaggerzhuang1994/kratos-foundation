package redis

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	foundationlock "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	goredis "github.com/redis/go-redis/v9"
)

type defaultLockerManager struct {
	client *goredis.Client
}

func (m defaultLockerManager) Default() *goredis.Client { return m.client }

func (m defaultLockerManager) Connection(string) (*goredis.Client, error) {
	return nil, errors.New("named connection must not be used")
}

func TestNewDefaultUsesManagerDefaultConnectionAndOptions(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })

	created, err := NewDefault(defaultLockerManager{client: client})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := created.(*locker)
	if !ok || got.client == nil || got.keyPrefix != defaultKeyPrefix || got.retryInterval != defaultRetryInterval {
		t.Fatalf("NewDefault() = %#v", created)
	}
}

// These tests kill mutations that silently accept invalid option/request values
// before any Redis call, or turn an absent lease into a successful operation.
func TestResolveOptions(t *testing.T) {
	got, err := resolveOptions(WithConnection(" named "), WithKeyPrefix("tenant:"), WithRetryInterval(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if got.connection != "named" || got.keyPrefix != "tenant:" || got.retryInterval != time.Millisecond {
		t.Fatalf("options = %#v", got)
	}
	for _, values := range [][]Option{{nil}, {WithRetryInterval(0)}, {WithRetryInterval(-time.Nanosecond)}} {
		if _, err := resolveOptions(values...); err == nil {
			t.Fatal("resolveOptions error = nil")
		}
	}
}

func TestValidateRequestRejectsBoundaryFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		ctx context.Context
		key string
		ttl time.Duration
	}{
		{ctx: canceled, key: "key", ttl: time.Millisecond},
		{ctx: context.Background(), key: "", ttl: time.Millisecond},
		{ctx: context.Background(), key: " key", ttl: time.Millisecond},
		{ctx: context.Background(), key: "key", ttl: time.Millisecond - time.Nanosecond},
	} {
		if err := validateRequest(test.ctx, test.key, test.ttl); err == nil {
			t.Fatalf("validateRequest(%q, %s) error = nil", test.key, test.ttl)
		}
	}
	if err := validateRequest(context.Background(), "key", time.Millisecond); err != nil {
		t.Fatalf("minimum TTL rejected: %v", err)
	}
}

func TestNilLockerAndLeaseReturnContractErrors(t *testing.T) {
	var locker *locker
	if _, err := locker.TryLock(context.Background(), "key", time.Millisecond); err == nil {
		t.Fatal("nil locker TryLock error = nil")
	}
	var lease *lease
	if lease.Key() != "" {
		t.Fatal("nil lease key is not empty")
	}
	if _, err := lease.TTL(context.Background()); !errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("TTL error = %v", err)
	}
	if err := lease.Refresh(context.Background(), time.Millisecond); !errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("Refresh error = %v", err)
	}
	if err := lease.Unlock(context.Background()); !errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("Unlock error = %v", err)
	}
}

type lockScriptOperation string

const (
	lockScriptObtain  lockScriptOperation = "obtain"
	lockScriptTTL     lockScriptOperation = "ttl"
	lockScriptRefresh lockScriptOperation = "refresh"
	lockScriptRelease lockScriptOperation = "release"
)

type lockScriptResult struct {
	value any
	err   error
}

type lockScriptCall struct {
	operation lockScriptOperation
	keys      []string
	args      []any
}

// fakeLockRedisClient replaces only the external Redis script executor. The
// redislock client, token handling, and foundation adapter remain real.
type fakeLockRedisClient struct {
	mu        sync.Mutex
	responses map[lockScriptOperation][]lockScriptResult
	calls     []lockScriptCall
	called    chan lockScriptOperation
}

func newFakeLockRedisClient(
	responses map[lockScriptOperation][]lockScriptResult,
) *fakeLockRedisClient {
	cloned := make(map[lockScriptOperation][]lockScriptResult, len(responses))
	for operation, results := range responses {
		cloned[operation] = append([]lockScriptResult(nil), results...)
	}
	return &fakeLockRedisClient{
		responses: cloned,
		called:    make(chan lockScriptOperation, 16),
	}
}

type fakeRedisProtocolError string

func (err fakeRedisProtocolError) Error() string { return string(err) }
func (fakeRedisProtocolError) RedisError()       {}

func (client *fakeLockRedisClient) Eval(
	ctx context.Context,
	script string,
	keys []string,
	args ...any,
) *goredis.Cmd {
	operation, err := classifyLockScript(script)
	if err != nil {
		return goredis.NewCmdResult(nil, err)
	}

	client.mu.Lock()
	client.calls = append(client.calls, lockScriptCall{
		operation: operation,
		keys:      append([]string(nil), keys...),
		args:      append([]any(nil), args...),
	})
	results := client.responses[operation]
	var result lockScriptResult
	if len(results) == 0 {
		result.err = fmt.Errorf("unexpected %s script call", operation)
	} else {
		result = results[0]
		client.responses[operation] = results[1:]
	}
	client.mu.Unlock()

	select {
	case client.called <- operation:
	default:
	}
	return goredis.NewCmdResult(result.value, result.err)
}

func (client *fakeLockRedisClient) EvalSha(
	context.Context,
	string,
	[]string,
	...any,
) *goredis.Cmd {
	// redis.Script.Run falls back to Eval when Redis reports a missing script.
	return goredis.NewCmdResult(
		nil,
		fakeRedisProtocolError("NOSCRIPT fixture script is not loaded"),
	)
}

func (client *fakeLockRedisClient) EvalRO(
	ctx context.Context,
	script string,
	keys []string,
	args ...any,
) *goredis.Cmd {
	return client.Eval(ctx, script, keys, args...)
}

func (client *fakeLockRedisClient) EvalShaRO(
	ctx context.Context,
	sha string,
	keys []string,
	args ...any,
) *goredis.Cmd {
	return client.EvalSha(ctx, sha, keys, args...)
}

func (*fakeLockRedisClient) ScriptExists(
	context.Context,
	...string,
) *goredis.BoolSliceCmd {
	return goredis.NewBoolSliceResult(nil, errors.New("unexpected ScriptExists call"))
}

func (*fakeLockRedisClient) ScriptLoad(context.Context, string) *goredis.StringCmd {
	return goredis.NewStringResult("", errors.New("unexpected ScriptLoad call"))
}

func (client *fakeLockRedisClient) callsFor(operation lockScriptOperation) []lockScriptCall {
	client.mu.Lock()
	defer client.mu.Unlock()
	calls := make([]lockScriptCall, 0, len(client.calls))
	for _, call := range client.calls {
		if call.operation == operation {
			calls = append(calls, call)
		}
	}
	return calls
}

func classifyLockScript(script string) (lockScriptOperation, error) {
	switch {
	case strings.Contains(script, "getrange"):
		return lockScriptObtain, nil
	case strings.Contains(script, "pexpire"):
		return lockScriptRefresh, nil
	case strings.Contains(script, "pttl"):
		return lockScriptTTL, nil
	case strings.Contains(script, "del"):
		return lockScriptRelease, nil
	default:
		return "", errors.New("unknown redislock script")
	}
}

func TestTryLockReturnsLeaseWithLogicalKey(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{value: "OK"}},
	})
	locker := newWithClient(client, options{
		keyPrefix:     "tenant:",
		retryInterval: time.Millisecond,
	})

	lease, err := locker.TryLock(context.Background(), "job", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.Key() != "job" {
		t.Fatalf("TryLock() lease = %#v, key = %q", lease, lease.Key())
	}
	calls := client.callsFor(lockScriptObtain)
	if len(calls) != 1 || len(calls[0].keys) != 1 || calls[0].keys[0] != "tenant:job" {
		t.Fatalf("obtain calls = %#v", calls)
	}
	if len(calls[0].args) != 3 || calls[0].args[2] != "250" {
		t.Fatalf("obtain args = %#v", calls[0].args)
	}
}

func TestTryLockMapsContentionToStableError(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{err: goredis.Nil}},
	})
	locker := newWithClient(client, defaultOptions())

	lease, err := locker.TryLock(context.Background(), "job", time.Second)
	if lease != nil || !errors.Is(err, foundationlock.ErrNotAcquired) {
		t.Fatalf("TryLock() = (%v, %v), want ErrNotAcquired", lease, err)
	}
}

func TestTryLockWrapsRedisError(t *testing.T) {
	dependencyErr := errors.New("redis unavailable")
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{err: dependencyErr}},
	})
	locker := newWithClient(client, defaultOptions())

	lease, err := locker.TryLock(context.Background(), "job", time.Second)
	if lease != nil || !errors.Is(err, dependencyErr) ||
		!strings.Contains(err.Error(), "obtain redis lock") {
		t.Fatalf("TryLock() = (%v, %v), want wrapped dependency error", lease, err)
	}
}

func TestLockRetriesContentionAndReturnsLease(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{err: goredis.Nil}, {value: "OK"}},
	})
	locker := newWithClient(client, options{
		keyPrefix:     "lock:",
		retryInterval: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := locker.Lock(ctx, "job", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil || lease.Key() != "job" {
		t.Fatalf("Lock() lease = %#v", lease)
	}
	if calls := client.callsFor(lockScriptObtain); len(calls) != 2 {
		t.Fatalf("obtain calls = %d, want 2", len(calls))
	}
}

func TestLockStopsRetryWhenContextCanceled(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{err: goredis.Nil}},
	})
	locker := newWithClient(client, options{
		keyPrefix:     "lock:",
		retryInterval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		lease foundationlock.Lease
		err   error
	}
	done := make(chan result, 1)
	go func() {
		lease, err := locker.Lock(ctx, "job", time.Second)
		done <- result{lease: lease, err: err}
	}()

	select {
	case operation := <-client.called:
		if operation != lockScriptObtain {
			t.Fatalf("first script operation = %s", operation)
		}
	case <-time.After(time.Second):
		t.Fatal("Lock did not attempt to obtain the lease")
	}
	cancel()

	select {
	case got := <-done:
		if got.lease != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Lock() after cancel = (%v, %v)", got.lease, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Lock did not stop after context cancellation")
	}
	if calls := client.callsFor(lockScriptObtain); len(calls) != 1 {
		t.Fatalf("obtain calls after cancel = %d, want 1", len(calls))
	}
}

func TestLockReturnsDependencyErrorWithoutRetry(t *testing.T) {
	dependencyErr := errors.New("redis unavailable")
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain: {{err: dependencyErr}},
	})
	locker := newWithClient(client, options{
		keyPrefix:     "lock:",
		retryInterval: time.Millisecond,
	})

	lease, err := locker.Lock(context.Background(), "job", time.Second)
	if lease != nil || !errors.Is(err, dependencyErr) {
		t.Fatalf("Lock() = (%v, %v), want dependency error", lease, err)
	}
	if calls := client.callsFor(lockScriptObtain); len(calls) != 1 {
		t.Fatalf("dependency failure was retried %d times", len(calls))
	}
}

func TestLeaseTTLRefreshAndUnlockSucceed(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain:  {{value: "OK"}},
		lockScriptTTL:     {{value: int64(275)}},
		lockScriptRefresh: {{value: int64(1)}},
		lockScriptRelease: {{value: int64(1)}},
	})
	locker := newWithClient(client, options{
		keyPrefix:     "tenant:",
		retryInterval: time.Millisecond,
	})
	lease, err := locker.TryLock(context.Background(), "job", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	ttl, err := lease.TTL(context.Background())
	if err != nil || ttl != 275*time.Millisecond {
		t.Fatalf("TTL() = (%s, %v)", ttl, err)
	}
	if err := lease.Refresh(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if err := lease.Unlock(context.Background()); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}

	for _, operation := range []lockScriptOperation{
		lockScriptTTL,
		lockScriptRefresh,
		lockScriptRelease,
	} {
		calls := client.callsFor(operation)
		if len(calls) != 1 || len(calls[0].keys) != 1 || calls[0].keys[0] != "tenant:job" {
			t.Fatalf("%s calls = %#v", operation, calls)
		}
	}
	refreshCalls := client.callsFor(lockScriptRefresh)
	if len(refreshCalls[0].args) != 2 || refreshCalls[0].args[1] != "500" {
		t.Fatalf("refresh args = %#v", refreshCalls[0].args)
	}
}

func TestLeaseMapsLostOwnership(t *testing.T) {
	client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
		lockScriptObtain:  {{value: "OK"}},
		lockScriptTTL:     {{value: int64(-3)}},
		lockScriptRefresh: {{value: int64(0)}},
		lockScriptRelease: {{value: int64(0)}},
	})
	locker := newWithClient(client, defaultOptions())
	lease, err := locker.TryLock(context.Background(), "job", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if ttl, err := lease.TTL(context.Background()); ttl != 0 ||
		!errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("TTL() = (%s, %v), want ErrNotHeld", ttl, err)
	}
	if err := lease.Refresh(context.Background(), time.Second); !errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("Refresh() error = %v, want ErrNotHeld", err)
	}
	if err := lease.Unlock(context.Background()); !errors.Is(err, foundationlock.ErrNotHeld) {
		t.Fatalf("Unlock() error = %v, want ErrNotHeld", err)
	}
}

func TestLeaseWrapsRedisErrors(t *testing.T) {
	tests := []struct {
		name      string
		operation lockScriptOperation
		prefix    string
		invoke    func(foundationlock.Lease) error
	}{
		{
			name:      "ttl",
			operation: lockScriptTTL,
			prefix:    "read redis lock TTL",
			invoke: func(lease foundationlock.Lease) error {
				_, err := lease.TTL(context.Background())
				return err
			},
		},
		{
			name:      "refresh",
			operation: lockScriptRefresh,
			prefix:    "refresh redis lock",
			invoke: func(lease foundationlock.Lease) error {
				return lease.Refresh(context.Background(), time.Second)
			},
		},
		{
			name:      "unlock",
			operation: lockScriptRelease,
			prefix:    "release redis lock",
			invoke: func(lease foundationlock.Lease) error {
				return lease.Unlock(context.Background())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencyErr := errors.New("redis unavailable")
			client := newFakeLockRedisClient(map[lockScriptOperation][]lockScriptResult{
				lockScriptObtain: {{value: "OK"}},
				test.operation:   {{err: dependencyErr}},
			})
			locker := newWithClient(client, defaultOptions())
			lease, err := locker.TryLock(context.Background(), "job", time.Second)
			if err != nil {
				t.Fatal(err)
			}

			err = test.invoke(lease)
			if !errors.Is(err, dependencyErr) || !strings.Contains(err.Error(), test.prefix) {
				t.Fatalf("operation error = %v, want wrapped dependency error", err)
			}
		})
	}
}

// TestExternalLockLeaseUsage 通过真实 Redis 脚本验证公共租约契约；不使用脚本返回值替身。
func TestExternalLockLeaseUsage(t *testing.T) {
	address := os.Getenv("FOUNDATION_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set FOUNDATION_TEST_REDIS_ADDR to run real Redis lease integration tests")
	}
	client := goredis.NewClient(&goredis.Options{
		Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: time.Second, ContextTimeoutEnabled: true,
	})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	// 每个场景拥有独立前缀，只清理明确属于该场景的键，不影响其他测试或业务数据。
	newLocker := func(t *testing.T) (foundationlock.Locker, string) {
		t.Helper()
		prefix := "foundation:test:lock:" + rand.Text() + ":"
		created, err := New(defaultLockerManager{client: client},
			WithKeyPrefix(prefix), WithRetryInterval(time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := client.Del(cleanupCtx, prefix+"order", prefix+"other").Err(); err != nil {
				t.Errorf("cleanup lease keys: %v", err)
			}
		})
		return created, prefix
	}
	acquire := func(t *testing.T, locker foundationlock.Locker, key string) foundationlock.Lease {
		t.Helper()
		lease, err := locker.TryLock(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return lease
	}
	assertHeld := func(t *testing.T, lease foundationlock.Lease, maximum time.Duration) {
		t.Helper()
		ttl, err := lease.TTL(ctx)
		if err != nil || ttl <= 0 || ttl > maximum {
			t.Fatalf("TTL = %v, %v; want positive TTL <= %v", ttl, err, maximum)
		}
	}
	assertLost := func(t *testing.T, lease foundationlock.Lease) {
		t.Helper()
		if ttl, err := lease.TTL(ctx); ttl != 0 || !errors.Is(err, foundationlock.ErrNotHeld) {
			t.Fatalf("lost TTL = %v, %v", ttl, err)
		}
		if err := lease.Refresh(ctx, time.Minute); !errors.Is(err, foundationlock.ErrNotHeld) {
			t.Fatalf("lost Refresh = %v", err)
		}
		if err := lease.Unlock(ctx); !errors.Is(err, foundationlock.ErrNotHeld) {
			t.Fatalf("lost Unlock = %v", err)
		}
	}

	t.Run("acquire_refresh_release", func(t *testing.T) {
		locker, prefix := newLocker(t)
		lease := acquire(t, locker, "order")
		if lease.Key() != "order" {
			t.Fatalf("logical key = %q", lease.Key())
		}
		assertHeld(t, lease, time.Minute)
		if err := lease.Refresh(ctx, 2*time.Minute); err != nil {
			t.Fatal(err)
		}
		if ttl, err := lease.TTL(ctx); err != nil || ttl <= time.Minute || ttl > 2*time.Minute {
			t.Fatalf("refreshed TTL = %v, %v", ttl, err)
		}
		if err := lease.Unlock(ctx); err != nil {
			t.Fatal(err)
		}
		if count, err := client.Exists(ctx, prefix+"order").Result(); err != nil || count != 0 {
			t.Fatalf("released key count = %d, %v", count, err)
		}
		assertLost(t, lease)
	})

	t.Run("contention_release_and_reacquire", func(t *testing.T) {
		locker, _ := newLocker(t)
		first := acquire(t, locker, "order")
		if got, err := locker.TryLock(ctx, "order", time.Minute); got != nil || !errors.Is(err, foundationlock.ErrNotAcquired) {
			t.Fatalf("contended TryLock = %v, %v", got, err)
		}
		if err := first.Unlock(ctx); err != nil {
			t.Fatal(err)
		}
		second, err := locker.Lock(ctx, "order", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		// 旧租约不能查询、续租或删除新持有者的锁。
		assertLost(t, first)
		assertHeld(t, second, time.Minute)
	})

	t.Run("distinct_keys_are_independent", func(t *testing.T) {
		locker, _ := newLocker(t)
		order := acquire(t, locker, "order")
		other := acquire(t, locker, "other")
		if err := order.Unlock(ctx); err != nil {
			t.Fatal(err)
		}
		assertHeld(t, other, time.Minute)
	})

	t.Run("same_key_different_namespaces", func(t *testing.T) {
		first, _ := newLocker(t)
		second, _ := newLocker(t)
		lease := acquire(t, first, "order")
		other := acquire(t, second, "order")
		if err := lease.Unlock(ctx); err != nil {
			t.Fatal(err)
		}
		assertHeld(t, other, time.Minute)
	})

	t.Run("expired_owner_cannot_release_replacement", func(t *testing.T) {
		locker, prefix := newLocker(t)
		old := acquire(t, locker, "order")
		// 主动令 Redis 键立即过期，确定性模拟租约到期，不依赖 sleep 或真实时间推进。
		if expired, err := client.PExpire(ctx, prefix+"order", 0).Result(); err != nil || !expired {
			t.Fatalf("expire lease = %v, %v", expired, err)
		}
		assertLost(t, old)
		replacement := acquire(t, locker, "order")
		assertLost(t, old)
		assertHeld(t, replacement, time.Minute)
	})

	t.Run("wait_deadline_preserves_current_owner", func(t *testing.T) {
		locker, _ := newLocker(t)
		owner := acquire(t, locker, "order")
		waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		defer stop()
		if got, err := locker.Lock(waitCtx, "order", time.Minute); got != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting Lock = %v, %v", got, err)
		}
		assertHeld(t, owner, time.Minute)
	})

	t.Run("invalid_refresh_preserves_lease", func(t *testing.T) {
		locker, _ := newLocker(t)
		lease := acquire(t, locker, "order")
		for _, ttl := range []time.Duration{0, -time.Second, time.Millisecond - time.Nanosecond} {
			if err := lease.Refresh(ctx, ttl); err == nil {
				t.Fatalf("Refresh(%s) unexpectedly succeeded", ttl)
			}
		}
		assertHeld(t, lease, time.Minute)
	})

	t.Run("canceled_acquisition_does_not_create_key", func(t *testing.T) {
		locker, prefix := newLocker(t)
		canceled, stop := context.WithCancel(ctx)
		stop()
		for _, obtain := range []func(context.Context, string, time.Duration) (foundationlock.Lease, error){locker.Lock, locker.TryLock} {
			if lease, err := obtain(canceled, "order", time.Minute); lease != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled acquisition = %v, %v", lease, err)
			}
		}
		if count, err := client.Exists(ctx, prefix+"order").Result(); err != nil || count != 0 {
			t.Fatalf("canceled acquisition key count = %d, %v", count, err)
		}
	})
}
