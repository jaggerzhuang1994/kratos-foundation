package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	foundationlock "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

type lockCoordinator struct {
	locker           foundationlock.Locker
	keyPrefix        string
	leaseTTL         time.Duration
	refreshInterval  time.Duration
	operationTimeout time.Duration
}

// NewLockCoordinator 将分布式 Locker 适配为任务并发协调器。
func NewLockCoordinator(
	locker foundationlock.Locker,
	config LockCoordinatorConfig,
) (ConcurrencyCoordinator, error) {
	resolved, err := resolveLockCoordinatorConfig(config)
	if err != nil {
		return nil, err
	}
	return &lockCoordinator{
		locker:           locker,
		keyPrefix:        resolved.KeyPrefix,
		leaseTTL:         resolved.LeaseTTL,
		refreshInterval:  resolved.RefreshInterval,
		operationTimeout: resolved.OperationTimeout,
	}, nil
}

// Acquire 等待底层锁可用，再返回带自动续租的执行守卫。
func (c *lockCoordinator) Acquire(
	ctx context.Context,
	key string,
) (ExecutionGuard, error) {
	return c.acquire(ctx, key, false)
}

// TryAcquire 只尝试一次底层锁，不等待已有执行者退出。
func (c *lockCoordinator) TryAcquire(
	ctx context.Context,
	key string,
) (ExecutionGuard, error) {
	return c.acquire(ctx, key, true)
}

// acquire 统一校验输入、映射锁错误并启动租约监视。
func (c *lockCoordinator) acquire(
	ctx context.Context,
	key string,
	try bool,
) (ExecutionGuard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("job coordination key is required")
	}

	lockKey := c.keyPrefix + key
	var (
		lease foundationlock.Lease
		err   error
	)
	if try {
		lease, err = c.locker.TryLock(ctx, lockKey, c.leaseTTL)
	} else {
		lease, err = c.locker.Lock(ctx, lockKey, c.leaseTTL)
	}
	if errors.Is(err, foundationlock.ErrNotAcquired) {
		return nil, ErrExecutionInProgress
	}
	if err != nil {
		return nil, fmt.Errorf("acquire job execution lock %q: %w", key, err)
	}
	if lease == nil {
		return nil, fmt.Errorf("acquire job execution lock %q: nil lease", key)
	}
	return newLockExecutionGuard(ctx, key, lease, c), nil
}

const (
	defaultCoordinatorKeyPrefix = "job:"
	defaultCoordinatorLeaseTTL  = 30 * time.Second
)

// LockCoordinatorConfig 控制锁租约和续租监视周期。
type LockCoordinatorConfig struct {
	// KeyPrefix 隔离不同环境或应用中的同名任务。
	KeyPrefix string
	// LeaseTTL 是每次成功续租后剩余的锁租期。
	LeaseTTL time.Duration
	// RefreshInterval 控制租约刷新频率。
	RefreshInterval time.Duration
	// OperationTimeout 限制单次刷新与释放调用的最长时间。
	OperationTimeout time.Duration
}

// resolveLockCoordinatorConfig 填充安全默认值，并保证刷新在租约失效前有完成窗口。
func resolveLockCoordinatorConfig(
	config LockCoordinatorConfig,
) (LockCoordinatorConfig, error) {
	config.KeyPrefix = strings.TrimSpace(config.KeyPrefix)
	if config.KeyPrefix == "" {
		config.KeyPrefix = defaultCoordinatorKeyPrefix
	}
	if config.LeaseTTL < 0 {
		return LockCoordinatorConfig{}, errors.New("job coordinator lease TTL must be positive")
	}
	if config.LeaseTTL == 0 {
		config.LeaseTTL = defaultCoordinatorLeaseTTL
	}
	if config.RefreshInterval < 0 {
		return LockCoordinatorConfig{}, errors.New(
			"job coordinator refresh interval must be positive",
		)
	}
	if config.RefreshInterval == 0 {
		config.RefreshInterval = config.LeaseTTL / 3
	}
	if config.RefreshInterval <= 0 || config.RefreshInterval >= config.LeaseTTL {
		return LockCoordinatorConfig{}, errors.New(
			"job coordinator refresh interval must be shorter than lease TTL",
		)
	}
	if config.OperationTimeout < 0 {
		return LockCoordinatorConfig{}, errors.New(
			"job coordinator operation timeout must be positive",
		)
	}
	if config.OperationTimeout == 0 {
		config.OperationTimeout = (config.LeaseTTL - config.RefreshInterval) / 2
		if config.OperationTimeout > 5*time.Second {
			config.OperationTimeout = 5 * time.Second
		}
	}
	if config.OperationTimeout <= 0 ||
		config.OperationTimeout >= config.LeaseTTL-config.RefreshInterval {
		return LockCoordinatorConfig{}, errors.New(
			"job coordinator operation timeout must finish before lease expiry",
		)
	}
	return config, nil
}
