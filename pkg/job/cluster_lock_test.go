package job

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

// memoryLocker 是进程内租约，模拟多个实例共享的 Redis 锁；refreshErr 非 nil 时续租失败。
type memoryLocker struct {
	mu         sync.Mutex
	held       map[string]time.Time
	keys       []string
	tryErr     error
	refreshErr error
	unlocks    atomic.Int32
}

func (l *memoryLocker) Lock(ctx context.Context, key string, ttl time.Duration) (lock.Lease, error) {
	return l.TryLock(ctx, key, ttl)
}

func (l *memoryLocker) TryLock(_ context.Context, key string, ttl time.Duration) (lock.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	if l.tryErr != nil {
		return nil, l.tryErr
	}
	if l.held == nil {
		l.held = map[string]time.Time{}
	}
	if until, ok := l.held[key]; ok && time.Now().Before(until) {
		return nil, lock.ErrNotAcquired
	}
	l.held[key] = time.Now().Add(ttl)
	return &memoryLease{locker: l, key: key}, nil
}

type memoryLease struct {
	locker *memoryLocker
	key    string
}

func (l *memoryLease) Key() string                                { return l.key }
func (l *memoryLease) TTL(context.Context) (time.Duration, error) { return 0, nil }
func (l *memoryLease) Refresh(_ context.Context, ttl time.Duration) error {
	l.locker.mu.Lock()
	defer l.locker.mu.Unlock()
	if l.locker.refreshErr != nil {
		return l.locker.refreshErr
	}
	l.locker.held[l.key] = time.Now().Add(ttl)
	return nil
}
func (l *memoryLease) Unlock(context.Context) error { l.locker.unlocks.Add(1); return nil }

func clusterCronRun(t *testing.T, locker lock.Locker, key string, ttl time.Duration, task Task) (func(context.Context) error, *admissionMetricsRecorder) {
	t.Helper()
	spec := NewSpec().RegisterCron("settle", "@hourly", task, WithClusterLock(locker, key, ttl)).(*Spec)
	metrics := &admissionMetricsRecorder{}
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), &testScheduler{}, newScheduleParser(testModuleLog(t)), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	return manager.cronJobs[0].managedJob.job.Run, metrics
}

// 共享同一 Locker 的实例在同一触发点只执行一次；执行结束后不释放租约，晚到的副本也会跳过。
func TestClusterLockRunsOncePerTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		locker := &memoryLocker{}
		var runs atomic.Int32
		task := TaskFunc(func(context.Context) error { runs.Add(1); return nil })
		first, firstMetrics := clusterCronRun(t, locker, "", 30*time.Second, task)
		second, secondMetrics := clusterCronRun(t, locker, "", 30*time.Second, task)
		if err := first(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second) // 模拟副本时钟偏差：另一实例稍晚触发同一轮。
		if err := second(context.Background()); err != nil {
			t.Fatal(err)
		}
		if runs.Load() != 1 || secondMetrics.skipped["cluster_locked"] != 1 || firstMetrics.skipped["cluster_locked"] != 0 {
			t.Fatalf("runs=%d skipped=%v/%v", runs.Load(), firstMetrics.skipped, secondMetrics.skipped)
		}
		if locker.unlocks.Load() != 0 || locker.keys[0] != "job:settle" {
			t.Fatalf("unlocks=%d keys=%v", locker.unlocks.Load(), locker.keys)
		}
		// 租约过期后的下一轮由任一实例再次执行。
		time.Sleep(30 * time.Second)
		if err := second(context.Background()); err != nil || runs.Load() != 2 {
			t.Fatalf("next trigger runs=%d err=%v", runs.Load(), err)
		}
	})
}

// 长任务执行期间续租；续租失败说明租约已被他人取得，任务 Context 立即取消并报告。
func TestClusterLockRefreshesAndCancelsWhenLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		locker := &memoryLocker{}
		run, _ := clusterCronRun(t, locker, "orders:settle", 3*time.Second, TaskFunc(func(ctx context.Context) error {
			time.Sleep(10 * time.Second) // 超过 TTL，依赖续租保持独占。
			if ctx.Err() != nil {
				return ctx.Err()
			}
			locker.mu.Lock()
			locker.refreshErr = lock.ErrNotHeld
			locker.mu.Unlock()
			<-ctx.Done()
			return ctx.Err()
		}))
		other, otherMetrics := clusterCronRun(t, locker, "orders:settle", 3*time.Second, TaskFunc(func(context.Context) error { return nil }))
		done := make(chan error, 1)
		go func() { done <- run(context.Background()) }()
		time.Sleep(5 * time.Second)
		if err := other(context.Background()); err != nil || otherMetrics.skipped["cluster_locked"] != 1 {
			t.Fatalf("lease not kept alive: skipped=%v err=%v", otherMetrics.skipped, err)
		}
		err := <-done
		if !errors.Is(err, errClusterLockLost) || !errors.Is(err, context.Canceled) {
			t.Fatalf("lost lease error = %v", err)
		}
	})
}

// 取锁出错时本轮不执行，错误交给调用方；参数错误在构造时拒绝。
func TestClusterLockErrorsAndValidation(t *testing.T) {
	ran := false
	run, _ := clusterCronRun(t, &memoryLocker{tryErr: errors.New("redis down")}, "", time.Minute, TaskFunc(func(context.Context) error { ran = true; return nil }))
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "redis down") || ran {
		t.Fatalf("lock error = %v ran=%v", err, ran)
	}
	for _, tc := range []struct {
		locker lock.Locker
		ttl    time.Duration
		want   string
	}{{nil, time.Minute, "requires a locker"}, {&memoryLocker{}, 500 * time.Millisecond, "at least 1s"}} {
		spec := NewSpec().RegisterCron("bad", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithClusterLock(tc.locker, "", tc.ttl)).(*Spec)
		if _, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), &testScheduler{}, newScheduleParser(testModuleLog(t)), nil, disabledAdmissionMetrics); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validation error = %v, want %q", err, tc.want)
		}
	}
}
