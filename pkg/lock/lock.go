package lock

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotAcquired 表示 TryLock 发现锁正被其他持有者占用。
	ErrNotAcquired = errors.New("lock not acquired")
	// ErrNotHeld 表示租约已过期或属于其他持有者。
	ErrNotHeld = errors.New("lock not held")
)

// Locker 为逻辑键获取分布式租约。
type Locker interface {
	// Lock 等待取得锁，或在上下文结束时返回。
	Lock(context.Context, string, time.Duration) (Lease, error)
	// TryLock 只尝试获取一次锁。
	TryLock(context.Context, string, time.Duration) (Lease, error)
}

// Lease 表示在有限时长内持有分布式锁。
type Lease interface {
	// Key 返回传给 Locker 的逻辑键。
	Key() string
	// TTL 返回租约剩余时长。
	TTL(context.Context) (time.Duration, error)
	// Refresh 重设租约剩余时长。
	Refresh(context.Context, time.Duration) error
	// Unlock 在租约仍属于调用方时释放锁。
	Unlock(context.Context) error
}
