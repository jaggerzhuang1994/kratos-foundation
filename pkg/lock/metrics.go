package lock

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type observedLocker struct {
	// Locker 被观测包装的底层锁实现。
	Locker
	// name 用于指标标签的固定锁类别名。
	name string
	// operations 锁操作结果计数。
	operations metric.Int64Counter
	// duration 锁操作耗时。
	duration metric.Float64Histogram
	// held 底层 Unlock 成功时的持有时长，单位秒；过期或解锁失败不产生样本。
	held metric.Float64Histogram
}

// WithMetrics 包装 Locker，按固定业务名称记录操作结果；name 不得包含业务 ID 或锁键。
// 它借用底层 Locker 和 Provider，不负责关闭资源，也不改变并发、重试或续租策略。
func WithMetrics(locker Locker, name string, provider metrics.Provider) (Locker, error) {
	if name == "" || strings.TrimSpace(name) != name {
		return nil, errors.New("lock metrics name must be non-empty without surrounding whitespace")
	}
	meter := provider.Meter("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock")
	operations, err := meter.Int64Counter("lock_operations_total")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("lock_operation_duration_seconds",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60))
	if err != nil {
		return nil, err
	}
	held, err := meter.Float64Histogram("lock_released_hold_duration_seconds",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.01, .1, .5, 1, 5, 10, 30, 60, 300, 600, 1800, 3600))
	if err != nil {
		return nil, err
	}
	return &observedLocker{Locker: locker, name: name, operations: operations, duration: duration, held: held}, nil
}

func (l *observedLocker) Lock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	started := time.Now()
	lease, err := l.Locker.Lock(ctx, key, ttl)
	l.record(ctx, "lock", err, started)
	if err != nil {
		return lease, err
	}
	return &observedLease{Lease: lease, observer: l, acquired: time.Now()}, nil
}

func (l *observedLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	started := time.Now()
	lease, err := l.Locker.TryLock(ctx, key, ttl)
	l.record(ctx, "try_lock", err, started)
	if err != nil {
		return lease, err
	}
	return &observedLease{Lease: lease, observer: l, acquired: time.Now()}, nil
}

func (l *observedLocker) record(ctx context.Context, operation string, err error, started time.Time) {
	result := "success"
	switch {
	case errors.Is(err, ErrNotAcquired):
		result = "contended"
	case errors.Is(err, ErrNotHeld):
		result = "not_held"
	case errors.Is(err, context.DeadlineExceeded):
		result = "timeout"
	case errors.Is(err, context.Canceled):
		result = "canceled"
	case err != nil:
		result = "error"
	}
	attrs := metric.WithAttributes(attribute.String("lock_name", l.name),
		attribute.String("operation", operation), attribute.String("result", result))
	l.operations.Add(ctx, 1, attrs)
	l.duration.Record(ctx, time.Since(started).Seconds(), attrs)
}

// observedLease 记录租约观测数据；同步和所有者校验由底层租约负责。
type observedLease struct {
	// Lease 底层已取得的租约。
	Lease
	// observer 共享的锁指标及名称。
	observer *observedLocker
	// acquired 成功取得租约的时间，创建后不变，用于计算持有时长。
	acquired time.Time
}

func (l *observedLease) TTL(ctx context.Context) (time.Duration, error) {
	started := time.Now()
	ttl, err := l.Lease.TTL(ctx)
	l.observer.record(ctx, "ttl", err, started)
	return ttl, err
}

func (l *observedLease) Refresh(ctx context.Context, ttl time.Duration) error {
	started := time.Now()
	err := l.Lease.Refresh(ctx, ttl)
	l.observer.record(ctx, "refresh", err, started)
	return err
}

func (l *observedLease) Unlock(ctx context.Context) error {
	started := time.Now()
	err := l.Lease.Unlock(ctx)
	l.observer.record(ctx, "unlock", err, started)
	// 只有底层确认释放成功才记录。过期、释放失败和进程崩溃没有完整持有时长样本。
	if err == nil {
		l.observer.held.Record(ctx, time.Since(l.acquired).Seconds(),
			metric.WithAttributes(attribute.String("lock_name", l.observer.name)))
	}
	return err
}
