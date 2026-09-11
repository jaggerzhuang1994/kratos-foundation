package job

import (
	"context"
	"errors"
)

var (
	// ErrExecutionInProgress 表示 TryAcquire 发现同一任务已经由其他执行者持有。
	ErrExecutionInProgress = errors.New("job execution is already in progress")
	// ErrCoordinationLost 表示租约失效，当前执行者不再拥有可靠的独占权。
	ErrCoordinationLost = errors.New("job execution coordination lost")
)

// ConcurrencyCoordinator 协调一个逻辑任务在多个进程中的并发执行。
type ConcurrencyCoordinator interface {
	Acquire(context.Context, string) (ExecutionGuard, error)
	TryAcquire(context.Context, string) (ExecutionGuard, error)
}

// ExecutionGuard 持有一次协调执行；续租失败时 Context 会携带 ErrCoordinationLost。
type ExecutionGuard interface {
	Context() context.Context
	Release() error
}

// DefaultCoordinator 返回 nil，表示不启用分布式协调。
// 进程内并发策略仍由 Job 自身处理；分布式策略必须注入实际协调器。
func DefaultCoordinator() ConcurrencyCoordinator { return nil }
