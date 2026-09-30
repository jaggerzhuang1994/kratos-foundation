package app

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
)

// serverCallbacks 仅适配 Kratos Server 接口，启动与停止行为由 App 提供。
type serverCallbacks struct {
	// start 执行运行时启动逻辑。
	start func(context.Context) error
	// stop 执行运行时停止逻辑。
	stop func(context.Context) error
}

func (s *serverCallbacks) Start(ctx context.Context) error { return s.start(ctx) }
func (s *serverCallbacks) Stop(ctx context.Context) error  { return s.stop(ctx) }

type endpointServer struct {
	// Server 提供被包装运行时的启停能力。
	transport.Server
	// endpoint 保留端点查询，并将 SDK 启动前查询失败转为统一回滚。
	endpoint func() (*url.URL, error)
}

func (s *endpointServer) Endpoint() (*url.URL, error) { return s.endpoint() }

// wrapServer 将 App 的故障传播、完成计数和停止预算绑定到单个服务。
func (a *App) wrapServer(server transport.Server) transport.Server {
	var stopOnce sync.Once
	var stopErr error
	callbacks := &serverCallbacks{
		start: func(ctx context.Context) (err error) {
			defer func() { err = errors.Join(err, a.complete(ctx)) }()
			err = server.Start(ctx)
			switch {
			case err == ErrStopRequested:
				a.requestStop()
				err = a.stop()
			case err != nil && ctx.Err() == nil:
				if !isCancellationOnly(err) {
					a.recordFailure(err)
				}
				a.requestStop()
				err = errors.Join(err, a.stop())
			}
			return err
		},
		stop: func(ctx context.Context) error {
			// 每个服务仍使用独立 Once；重复 Stop 共享同一结果，不扩大同步粒度。
			stopOnce.Do(func() {
				stopCtx := ctx
				cancel := func() {}
				if timeout := a.stopTimeout(); timeout > 0 {
					stopCtx, cancel = context.WithTimeout(ctx, timeout)
				}
				defer cancel()
				stopErr = server.Stop(stopCtx)
				if !isCancellationOnly(stopErr) {
					a.recordFailure(stopErr)
				}
				stopErr = errors.Join(stopErr, a.complete(stopCtx))
			})
			return stopErr
		},
	}
	if endpoint, ok := server.(transport.Endpointer); ok {
		return &endpointServer{Server: callbacks, endpoint: func() (*url.URL, error) {
			resolved, err := endpoint.Endpoint()
			if err == nil {
				return resolved, nil
			}
			// SDK 在 BeforeStart 之前解析端点，失败时还没有 Start/Stop 协程。
			// 在这个入口回滚，才能同时覆盖 Foundation App 和底层 Kratos App 的 Run。
			ctx := kratos.NewContext(a.parent, a.App)
			return nil, a.stopBeforeStart(ctx, err)
		}}
	}
	return callbacks
}

// waitParent 把父上下文取消转为 App 停止请求，确保执行停止钩子和注销。
// 监听由 Kratos 启动，父上下文取消、Kratos 取消或 stopParent 均会使其退出。
func (a *App) waitParent(ctx context.Context) error {
	select {
	case <-a.parent.Done():
		return a.stop()
	case <-a.parentDone:
		return nil
	case <-ctx.Done():
		return nil
	}
}

// stopParent 幂等解除父上下文监听。
func (a *App) stopParent(context.Context) error {
	a.parentStopOnce.Do(func() { close(a.parentDone) })
	return nil
}

// initServers 在构造期登记服务完成次数；Run 开始后不再改变初始计数。
func (a *App) initServers(count int) {
	a.remaining = count * 2
	a.serversDone = make(chan struct{})
	if a.remaining == 0 {
		close(a.serversDone)
	}
}

// complete 消耗一个运行时完成信号，并由最后完成者触发最终停止钩子。
func (a *App) complete(ctx context.Context) error {
	a.serversMu.Lock()
	if a.remaining == 0 {
		a.serversMu.Unlock()
		return nil
	}
	a.remaining--
	last := a.remaining == 0
	if last {
		close(a.serversDone)
	}
	a.serversMu.Unlock()
	if last && a.isStopping() {
		return a.runAfterStop(ctx)
	}
	return nil
}

// wait 在故障收敛路径等待所有运行时退出，并受冻结后的停机预算约束。
func (a *App) wait(timeout time.Duration) error {
	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	select {
	case <-a.serversDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopAfterFailure 停止已经开始的应用，等待运行时退出后再执行最终钩子。
func (a *App) stopAfterFailure(ctx context.Context, cause error) error {
	a.recordFailure(cause)
	if errors.Is(cause, errAppStopping) {
		cause = a.failureOr(nil)
	} else {
		cause = a.failureOr(cause)
	}
	a.requestStop()
	stopErr := a.stop()
	waitErr := a.wait(a.stopTimeout())
	afterStopErr := a.runAfterStop(ctx)
	return errors.Join(cause, stopErr, waitErr, afterStopErr)
}

// startupAborter 只回收运行时尚未启动的资源，不启动或停止普通自定义运行时。
type startupAborter interface {
	AbortStartup(context.Context) error
}

// abortStartup 用本次冻结的预算逆序回滚全部原始运行时，包括当前查询失败的端点。
func (a *App) abortStartup(ctx context.Context) error {
	cleanupCtx := a.shutdownContext(ctx)
	cancel := func() {}
	if timeout := a.stopTimeout(); timeout > 0 {
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, timeout)
	}
	defer cancel()
	var errs []error
	aborted := 0
	for index := len(a.runtimes) - 1; index >= 0; index-- {
		if runtime, ok := a.runtimes[index].(startupAborter); ok {
			aborted++
			if err := runtime.AbortStartup(cleanupCtx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		a.logEvent(cleanupCtx, kratoslog.LevelError, "app.startup.abort.failed", "startup rollback failed", "runtimes", aborted, "error", err)
	} else {
		a.logEvent(cleanupCtx, kratoslog.LevelInfo, "app.startup.aborted", "startup rolled back", "runtimes", aborted)
	}
	return err
}

// stopBeforeStart 收敛启动前故障；先回滚预创建资源，再执行最终停止钩子。
func (a *App) stopBeforeStart(ctx context.Context, cause error) error {
	a.recordFailure(cause)
	if errors.Is(cause, errAppStopping) {
		cause = a.failureOr(nil)
	} else {
		cause = a.failureOr(cause)
	}
	a.requestStop()
	stopErr := a.stop()
	abortErr := a.abortStartup(ctx)
	afterStopErr := a.runAfterStop(ctx)
	return errors.Join(cause, stopErr, abortErr, afterStopErr)
}

// isCancellationOnly 只将所有叶子都为取消的错误树视为正常停止，保留合并的真实故障。
func isCancellationOnly(err error) bool {
	switch typed := err.(type) {
	case interface{ Unwrap() []error }:
		causes := typed.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isCancellationOnly(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return isCancellationOnly(typed.Unwrap())
	default:
		return errors.Is(err, context.Canceled)
	}
}
