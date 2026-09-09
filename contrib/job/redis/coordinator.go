// Package redis 组装 Redis Locker 与通用 Job 并发协调器。
package redis

import (
	"fmt"

	lockredis "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/lock/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
)

// NewLockCoordinator 使用 Redis Manager 构造 Job 并发协调器。
func NewLockCoordinator(
	manager foundationredis.Manager,
	config job.LockCoordinatorConfig,
	options ...lockredis.Option,
) (job.ConcurrencyCoordinator, error) {
	locker, err := lockredis.New(manager, options...)
	if err != nil {
		return nil, fmt.Errorf("create Redis job locker: %w", err)
	}
	coordinator, err := job.NewLockCoordinator(locker, config)
	if err != nil {
		return nil, fmt.Errorf("create Redis job coordinator: %w", err)
	}
	return coordinator, nil
}

// NewDefaultLockCoordinator 使用默认 Redis 连接和协调参数构造 Job 并发协调器。
func NewDefaultLockCoordinator(
	manager foundationredis.Manager,
) (job.ConcurrencyCoordinator, error) {
	return NewLockCoordinator(manager, job.LockCoordinatorConfig{})
}
