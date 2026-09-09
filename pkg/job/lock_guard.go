package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	foundationlock "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

type lockExecutionGuard struct {
	key              string
	lease            foundationlock.Lease
	executionCtx     context.Context
	cancelExecution  context.CancelCauseFunc
	stopWatch        context.CancelFunc
	watchDone        chan struct{}
	leaseTTL         time.Duration
	refreshInterval  time.Duration
	operationTimeout time.Duration

	releaseOnce sync.Once
	releaseErr  error
}

// newLockExecutionGuard 派生只在租约有效期间存活的任务上下文并启动续租监视。
func newLockExecutionGuard(
	parent context.Context,
	key string,
	lease foundationlock.Lease,
	coordinator *lockCoordinator,
) *lockExecutionGuard {
	executionCtx, cancelExecution := context.WithCancelCause(parent)
	watchCtx, stopWatch := context.WithCancel(parent)
	guard := &lockExecutionGuard{
		key:              key,
		lease:            lease,
		executionCtx:     executionCtx,
		cancelExecution:  cancelExecution,
		stopWatch:        stopWatch,
		watchDone:        make(chan struct{}),
		leaseTTL:         coordinator.leaseTTL,
		refreshInterval:  coordinator.refreshInterval,
		operationTimeout: coordinator.operationTimeout,
	}
	go guard.watch(watchCtx)
	return guard
}

// Context 返回与当前租约所有权绑定的执行上下文。
func (g *lockExecutionGuard) Context() context.Context {
	return g.executionCtx
}

// Release 幂等停止续租、释放锁，并保留第一次释放的结果。
func (g *lockExecutionGuard) Release() error {
	g.releaseOnce.Do(func() {
		g.cancelExecution(nil)
		g.stopWatch()
		ctx, cancel := context.WithTimeout(
			context.WithoutCancel(g.executionCtx),
			g.operationTimeout,
		)
		defer cancel()
		deadline, _ := ctx.Deadline()
		select {
		case <-g.watchDone:
		case <-ctx.Done():
			// 不并发 Unlock 正在刷新的租约，也不再派生阻塞 worker；由 TTL 回收。
			g.releaseErr = fmt.Errorf("stop execution lock %q refresh: %w", g.key, ctx.Err())
			return
		}
		if !time.Now().Before(deadline) {
			g.releaseErr = fmt.Errorf("stop execution lock %q refresh: %w", g.key, context.DeadlineExceeded)
			return
		}
		err := g.lease.Unlock(ctx)
		if errors.Is(err, foundationlock.ErrNotHeld) {
			g.releaseErr = fmt.Errorf(
				"%w: release execution lock %q: %w",
				ErrCoordinationLost,
				g.key,
				err,
			)
		} else if err != nil {
			g.releaseErr = fmt.Errorf("release execution lock %q: %w", g.key, err)
		}
	})
	return g.releaseErr
}

// watch 定期刷新租约；首次刷新失败即取消任务，防止失去锁后继续产生副作用。
func (g *lockExecutionGuard) watch(ctx context.Context) {
	defer close(g.watchDone)
	ticker := time.NewTicker(g.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
			timeoutDone := make(chan struct{})
			stopTimeout := context.AfterFunc(refreshCtx, func() {
				defer close(timeoutDone)
				if errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
					g.loseExecution(refreshCtx.Err())
				}
			})
			err := g.lease.Refresh(refreshCtx, g.leaseTTL)
			// 先停止或等待短超时回调，再读 deadline 状态。即使 Refresh 超时后
			// 晚到成功，也不能恢复已取消的执行权。正常返回不留下后续取消回调。
			if !stopTimeout() {
				<-timeoutDone
			}
			err = errors.Join(err, refreshCtx.Err())
			deadline, _ := refreshCtx.Deadline()
			if !time.Now().Before(deadline) {
				// 定时回调可能尚未获得调度，不能把恰好超时的成功当成有效续租。
				err = errors.Join(err, context.DeadlineExceeded)
			}
			cancel()
			if err == nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			g.loseExecution(err)
			return
		}
	}
}

// loseExecution 单向取消旧任务，不获取新租约，也不恢复旧上下文。
func (g *lockExecutionGuard) loseExecution(err error) {
	g.cancelExecution(fmt.Errorf("%w: refresh execution lock %q: %w", ErrCoordinationLost, g.key, err))
}
