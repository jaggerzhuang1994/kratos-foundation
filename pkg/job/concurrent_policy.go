package job

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConcurrentPolicy 决定同一周期任务重叠时继续、等待还是跳过。
type ConcurrentPolicy uint8

const (
	// AllowOverlap 允许同一任务的多次调用重叠执行。
	AllowOverlap ConcurrentPolicy = iota
	// DelayIfRunning 在当前进程内有界等待上一轮完成，容量由 WithMaxPendingRuns 决定。
	DelayIfRunning
	// SkipIfRunning 在当前进程已有同名任务运行时跳过本轮。
	SkipIfRunning
	// DelayIfDistributedRunning 在进程内限制竞争者数量，跨进程等待独占执行权。
	DelayIfDistributedRunning
	// SkipIfDistributedRunning 在其他进程持有执行权时跳过本轮。
	SkipIfDistributedRunning
)

// valid 判断策略值是否属于公开枚举范围。
func (p ConcurrentPolicy) valid() bool {
	return p <= SkipIfDistributedRunning
}

// distributed 判断策略是否需要跨进程协调器。
func (p ConcurrentPolicy) distributed() bool {
	return p == DelayIfDistributedRunning || p == SkipIfDistributedRunning
}

// concurrentMiddleware 把声明式并发策略转换为任务中间件。
func concurrentMiddleware(
	log moduleLog,
	policy ConcurrentPolicy,
	coordinator ConcurrencyCoordinator,
	key string,
	maxPendingRuns int,
	overflowHandler func(context.Context, DelayOverflow) error,
) Middleware {
	switch policy {
	case DelayIfRunning:
		return limitPendingRuns(log, DelayOverflow{Name: key, Policy: policy, MaxPendingRuns: maxPendingRuns}, overflowHandler, delayIfStillRunning(log))
	case SkipIfRunning:
		return skipIfStillRunning(log)
	case DelayIfDistributedRunning:
		return limitPendingRuns(log, DelayOverflow{Name: key, Policy: policy, MaxPendingRuns: maxPendingRuns}, overflowHandler, distributedConcurrent(log, coordinator, key, true))
	case SkipIfDistributedRunning:
		return distributedConcurrent(log, coordinator, key, false)
	default:
		return nil
	}
}

// limitPendingRuns 在等待执行权前限制本任务、本进程的进入数量，容量包含正在执行的一轮。
// 非阻塞申请名额；结束、取消和 panic 均通过 defer 归还，不在 Handler 或外部 Acquire 期间持锁。
func limitPendingRuns(
	log moduleLog,
	event DelayOverflow,
	overflowHandler func(context.Context, DelayOverflow) error,
	middleware Middleware,
) Middleware {
	maxPendingRuns := event.MaxPendingRuns
	return func(next Handler) Handler {
		execute := middleware(next)
		if maxPendingRuns < 0 {
			return execute
		}
		slots := make(chan struct{}, maxPendingRuns+1)
		return func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				return execute(ctx)
			default:
				warnConcurrent(log, ctx, "limitPendingRuns | job backlog full; trigger skipped", "max_pending_runs", maxPendingRuns)
				if overflowHandler != nil {
					return handleDelayOverflow(ctx, event, overflowHandler)
				}
				return nil
			}
		}
	}
}

// handleDelayOverflow 位于任务 recovery 中间件之外，单独隔离业务通知 panic。
// 此时未取得执行名额；错误交给 Cron 的最终错误入口，不在底层重复记录。
func handleDelayOverflow(ctx context.Context, event DelayOverflow, handler func(context.Context, DelayOverflow) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("delay overflow handler for job %q panicked: %v", event.Name, recovered)
		}
	}()
	if err := handler(ctx, event); err != nil {
		return fmt.Errorf("delay overflow handler for job %q: %w", event.Name, err)
	}
	return nil
}

// delayIfStillRunning 使用进程内令牌串行执行同一任务，并响应等待上下文取消。
func delayIfStillRunning(log moduleLog) Middleware {
	return func(next Handler) Handler {
		token := make(chan struct{}, 1)
		token <- struct{}{}
		return func(ctx context.Context) error {
			start := time.Now()
			select {
			case value := <-token:
				defer func() {
					token <- value
				}()
			case <-ctx.Done():
				return ctx.Err()
			}
			if duration := time.Since(start); duration > 5*time.Second {
				warnConcurrent(log, ctx, "job delayed", "duration", duration)
			}
			return next(ctx)
		}
	}
}

// skipIfStillRunning 使用非阻塞令牌在本轮重叠时直接跳过。
func skipIfStillRunning(log moduleLog) Middleware {
	return func(next Handler) Handler {
		token := make(chan struct{}, 1)
		token <- struct{}{}
		return func(ctx context.Context) error {
			select {
			case value := <-token:
				defer func() {
					token <- value
				}()
				return next(ctx)
			default:
				warnConcurrent(log, ctx, "job skipped")
				return nil
			}
		}
	}
}

// distributedConcurrent 在执行任务前取得跨进程守卫，并合并执行权丢失与释放错误。
func distributedConcurrent(
	log moduleLog,
	coordinator ConcurrencyCoordinator,
	key string,
	wait bool,
) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) error {
			start := time.Now()
			var (
				guard ExecutionGuard
				err   error
			)
			if wait {
				guard, err = coordinator.Acquire(ctx, key)
			} else {
				guard, err = coordinator.TryAcquire(ctx, key)
			}
			if !wait && errors.Is(err, ErrExecutionInProgress) {
				warnConcurrent(log, ctx, "distributed job skipped", "key", key)
				return nil
			}
			if err != nil {
				err = fmt.Errorf("coordinate job execution %q: %w", key, err)
				errorConcurrent(log, ctx, "job coordination failed", "key", key, "error", err)
				return err
			}
			if guard == nil {
				err = fmt.Errorf("coordinate job execution %q: nil execution guard", key)
				errorConcurrent(log, ctx, "job coordination failed", "key", key, "error", err)
				return err
			}
			released := false
			defer func() {
				if !released {
					// 正常路径会显式合并释放错误；这里只处理外层中间件意外 panic 的兜底路径。
					if releaseErr := guard.Release(); releaseErr != nil {
						errorConcurrent(
							log,
							ctx,
							"release job coordination after panic failed",
							"key",
							key,
							"error",
							releaseErr,
						)
					}
				}
			}()
			if wait {
				if duration := time.Since(start); duration > 5*time.Second {
					warnConcurrent(
						log,
						ctx,
						"distributed job delayed",
						"key",
						key,
						"duration",
						duration,
					)
				}
			}

			executionCtx := guard.Context()
			runErr := next(executionCtx)
			cause := context.Cause(executionCtx)
			if !errors.Is(cause, ErrCoordinationLost) {
				cause = nil
			}
			released = true
			releaseErr := guard.Release()
			if releaseErr != nil {
				releaseErr = fmt.Errorf(
					"release job execution coordination %q: %w",
					key,
					releaseErr,
				)
			}
			if cause != nil || releaseErr != nil {
				errorConcurrent(
					log,
					ctx,
					"job coordination lost",
					"key",
					key,
					"error",
					errors.Join(cause, releaseErr),
				)
			}
			return errors.Join(runErr, cause, releaseErr)
		}
	}
}

// warnConcurrent 为并发策略告警补齐任务上下文和结构化字段。
func warnConcurrent(log moduleLog, ctx context.Context, message string, keyvals ...any) {
	log.WithContext(ctx).With(keyvals...).Warn(message)
}

// errorConcurrent 为协调失败日志补齐任务上下文和结构化字段。
func errorConcurrent(log moduleLog, ctx context.Context, message string, keyvals ...any) {
	log.WithContext(ctx).With(keyvals...).Error(message)
}
