package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/errtree"
)

// Run 按以下顺序执行一次完整生命周期并返回汇总结果：
//
//  1. 解析服务端点，执行 BeforeStart；失败、父 Context 已取消或已请求停止时逆序回滚未启动资源。
//  2. 并发启动全部 Runtime，注册服务，执行 AfterStart 后发布 Ready。
//  3. 等待停止请求（Stop、系统信号、父 Context 取消、Runtime 故障或 ErrStopRequested）。
//  4. 执行一次 BeforeStop 与注销，按冻结预算并发停止 Runtime，等待全部 Start/Stop 返回后执行 AfterStop。
//
// 只有错误树全部为取消的结果视为正常停止；真实故障、停止钩子和注销错误均通过 errors.Join 返回。
func (a *App) Run() error {
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("application is already running")
	}
	// 信号与父 Context 取消在启动阶段同样转为统一停止，执行停止钩子和注销。
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, a.signals...)
	defer signal.Stop(signals)
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		select {
		case <-signals:
			_ = a.Stop()
		case <-a.parent.Done():
			_ = a.Stop()
		case <-watching:
		}
	}()

	instance, err := a.buildInstance()
	if err != nil {
		return a.abortStartup(err)
	}
	if err := a.parent.Err(); err != nil {
		return a.abortStartup(err)
	}
	if err := runStartHooks(a.runCtx, a.beforeStart); err != nil {
		return a.abortStartup(err)
	}
	if err := a.parent.Err(); err != nil {
		return a.abortStartup(err)
	}
	if a.isStopping() {
		return a.abortStartup(nil)
	}

	starts := a.startRuntimes()
	startupErr := a.register(instance)
	if startupErr == nil {
		startupErr = a.runAfterStart(a.runCtx)
	}
	if startupErr != nil {
		// 启动阶段失败时，等待运行时退出受冻结预算约束，避免不响应的 Start 阻塞故障上报。
		a.recordFailure(startupErr)
		a.requestStop()
		return a.shutdown(starts, true)
	}
	<-a.stoppingSignal
	return a.shutdown(starts, false)
}

// buildInstance 解析服务实例；有显式端点时不查询 Runtime，否则按登记顺序查询 Endpointer。
func (a *App) buildInstance() (*registry.ServiceInstance, error) {
	endpoints := make([]string, 0, len(a.endpoints))
	for _, endpoint := range a.endpoints {
		endpoints = append(endpoints, endpoint.String())
	}
	if len(endpoints) == 0 {
		for _, runtime := range a.runtimes {
			endpointer, ok := runtime.(transport.Endpointer)
			if !ok {
				continue
			}
			endpoint, err := endpointer.Endpoint()
			if err != nil {
				return nil, err
			}
			endpoints = append(endpoints, endpoint.String())
		}
	}
	instance := &registry.ServiceInstance{ID: a.id, Name: a.name, Version: a.version, Metadata: a.metadata, Endpoints: endpoints}
	a.mu.Lock()
	a.instance = instance
	a.mu.Unlock()
	return instance, nil
}

// startRuntimes 并发启动全部 Runtime；任何 Start 返回非 nil 都会请求统一停止。
func (a *App) startRuntimes() *sync.WaitGroup {
	var starts sync.WaitGroup
	for _, runtime := range a.runtimes {
		starts.Go(func() {
			err := runtime.Start(a.base)
			// ErrStopRequested 按精确值识别，包装或合并了它的错误仍视为故障。
			switch err {
			case nil:
				// 提前正常返回的 Runtime 不影响其他 Runtime，停机时仍会调用其 Stop。
			case ErrStopRequested:
				a.requestStop()
			default:
				if !errtree.Only(err, context.Canceled) {
					a.recordFailure(err)
				}
				a.requestStop()
			}
		})
	}
	return &starts
}

// shutdown 执行一次停止前钩子与注销，并发停止全部已启动的 Runtime，等待其返回后执行 AfterStop。
// bounded 为 true 时，整个等待受冻结预算约束，超时记录 DeadlineExceeded 后继续收尾。
func (a *App) shutdown(starts *sync.WaitGroup, bounded bool) error {
	_ = a.beginStop()
	timeout := a.stopTimeout()
	var deadline <-chan time.Time
	if bounded && timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	// 每个 Runtime.Stop 从调用时起分别获得冻结预算，不依赖登记顺序。
	var stops sync.WaitGroup
	for _, runtime := range a.runtimes {
		stops.Go(func() {
			ctx, cancel := a.budgetContext(timeout)
			defer cancel()
			if err := runtime.Stop(ctx); err != nil && !errtree.Only(err, context.Canceled) {
				a.recordFailure(err)
			}
		})
	}
	done := make(chan struct{})
	go func() {
		stops.Wait()
		starts.Wait()
		close(done)
	}()
	var waitErr error
	select {
	case <-done:
	case <-deadline:
		waitErr = fmt.Errorf("wait for runtimes to stop: %w", context.DeadlineExceeded)
	}
	return a.finish(waitErr)
}

// startupAborter 只回收运行时尚未启动的资源，不启动或停止普通自定义运行时。
type startupAborter interface {
	AbortStartup(context.Context) error
}

// abortStartup 在任何 Runtime 启动之前收敛故障：执行一次停止前钩子与注销，
// 用一份冻结预算逆序回滚全部原始 Runtime，最后执行 AfterStop。
// cause 为 nil 表示停止请求先于启动到达，不计为故障。
func (a *App) abortStartup(cause error) error {
	a.recordFailure(cause)
	a.requestStop()
	_ = a.beginStop()
	ctx, cancel := a.budgetContext(a.stopTimeout())
	defer cancel()
	var errs []error
	aborted := 0
	for index := len(a.runtimes) - 1; index >= 0; index-- {
		if runtime, ok := a.runtimes[index].(startupAborter); ok {
			aborted++
			// 一个回滚失败或预算耗尽后仍继续回滚其余资源。
			errs = append(errs, runtime.AbortStartup(ctx))
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		a.logEvent(ctx, kratoslog.LevelError, "app.startup.abort.failed", "startup rollback failed", "runtimes", aborted, "error", err)
	} else {
		a.logEvent(ctx, kratoslog.LevelInfo, "app.startup.aborted", "startup rolled back", "runtimes", aborted)
	}
	return a.finish(err)
}

// finish 执行 AfterStop，并按“真实故障、停止前钩子、注销、收尾附加错误、AfterStop”顺序汇总结果。
func (a *App) finish(extra error) error {
	afterStopErr := runStopHooks(a.base, a.afterStop)
	a.mu.Lock()
	err := errors.Join(errors.Join(a.failures...), a.beforeStopErr, a.deregisterErr, extra, afterStopErr)
	a.mu.Unlock()

	level, result, fields := kratoslog.LevelInfo, "success", []any(nil)
	if err != nil {
		if errtree.Only(err, context.Canceled) {
			result = "canceled"
		} else {
			level, result, fields = kratoslog.LevelError, "failed", []any{"error", err}
		}
	}
	a.logEvent(a.base, level, "app.stopped", "application stopped", append([]any{"result", result}, fields...)...)
	return err
}

// budgetContext 返回保留应用值、不受取消影响的清理 Context，并在预算为正时设置超时。
func (a *App) budgetContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(a.base, timeout)
	}
	return context.WithCancel(a.base)
}
