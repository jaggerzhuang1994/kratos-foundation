package app

import (
	"context"
	"errors"
	"time"
)

var errAppStopping = errors.New("application is stopping")

// setFinalError 注册所有停止钩子结束后才读取的最终错误来源。
func (a *App) setFinalError(fn func() error) {
	a.finalError = fn
}

// requestStop 只在首次停止请求时冻结超时，避免热更新改变正在进行的停机预算。
func (a *App) requestStop() {
	a.stopOnce.Do(func() {
		a.stopTime = a.stopPolicy.current()
		a.stopping.Store(true)
	})
}

// isStopping 报告应用是否已经进入停止阶段。
func (a *App) isStopping() bool {
	return a.stopping.Load()
}

// stopTimeout 返回首次停止时冻结的超时。
func (a *App) stopTimeout() time.Duration {
	a.requestStop()
	return a.stopTime
}

// recordFailure 保存首个真实故障，使并发关闭噪声不会覆盖根因。
func (a *App) recordFailure(err error) {
	if err == nil || errors.Is(err, errAppStopping) {
		return
	}
	a.failureMu.Lock()
	if a.failureErr == nil {
		a.failureErr = err
	}
	a.failureMu.Unlock()
}

// failureOr 将首个生命周期故障与当前回退错误合并。
func (a *App) failureOr(fallback error) error {
	a.failureMu.Lock()
	defer a.failureMu.Unlock()
	if a.failureErr != nil {
		return errors.Join(a.failureErr, fallback)
	}
	return fallback
}

// startupCallbackError 将统一停机取消的启动回调 Context 错误归类为停机噪声。
func (a *App) startupCallbackError(ctx context.Context, err error) error {
	if !a.isStopping() {
		return err
	}
	// 取消与真实故障可能被 errors.Join 合并；只归一化纯取消，避免吞掉注册或钩子失败。
	if ctx.Err() == context.Canceled && isCancellationOnly(err) {
		return errAppStopping
	}
	return err
}

// runBeforeStart 按声明顺序执行启动前钩子并在首个错误处停止。
func (a *App) runBeforeStart(ctx context.Context) error {
	return runStartHooks(ctx, a.beforeStart)
}

// runAfterStart 执行启动后钩子，并在并发停止请求出现时立即收敛。
func (a *App) runAfterStart(ctx context.Context) error {
	for _, hook := range a.afterStart {
		if a.isStopping() {
			return errAppStopping
		}
		if err := hook(ctx); err != nil {
			err = a.startupCallbackError(ctx, err)
			a.recordFailure(err)
			return err
		}
	}
	if a.isStopping() {
		return errAppStopping
	}
	a.ready.Store(true)
	return nil
}

// runBeforeStop 幂等执行停止前钩子并保存 Kratos 提供的停止 Context。
func (a *App) runBeforeStop(ctx context.Context) error {
	a.requestStop()
	a.contextMu.Lock()
	a.stopCtx = ctx
	a.contextMu.Unlock()
	a.beforeStopOnce.Do(func() {
		a.beforeStopErr = runStopHooks(ctx, a.beforeStop)
	})
	return a.beforeStopErr
}

// runAfterStop 确保停止前后钩子各执行一次，并汇总全部清理错误。
func (a *App) runAfterStop(ctx context.Context) error {
	a.afterStopOnce.Do(func() {
		beforeStopErr := a.runBeforeStop(ctx)
		hookCtx := a.shutdownContext(ctx)
		// Kratos 会忽略整个含 context.Canceled 的运行时错误树；最终钩子需重新
		// 汇总已记录的真实故障，避免 errors.Join 中的业务错误被当成正常停止。
		a.afterStopErr = a.failureOr(errors.Join(
			beforeStopErr,
			runStopHooks(hookCtx, a.afterStop),
		))
		if a.finalError != nil {
			a.afterStopErr = errors.Join(a.afterStopErr, a.finalError())
		}
	})
	return a.afterStopErr
}

// shutdownContext 返回不再受取消信号影响的清理 Context。
func (a *App) shutdownContext(fallback context.Context) context.Context {
	a.contextMu.RLock()
	ctx := a.stopCtx
	a.contextMu.RUnlock()
	if ctx != nil {
		// 停止 Context 可能在运行时退出前到期；AfterStop 仍需执行本地无阻塞清理，
		// 因此只保留其中的值，不传播取消状态或已经耗尽的截止时间。
		return context.WithoutCancel(ctx)
	}
	return context.WithoutCancel(fallback)
}

// runStartHooks 对启动钩子采用 fail-fast，避免在前置条件失败后继续启动。
func runStartHooks(ctx context.Context, hooks []HookFunc) error {
	for _, hook := range hooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}

// runStopHooks 尽量执行全部停止钩子，防止一个清理失败阻断后续资源释放。
func runStopHooks(ctx context.Context, hooks []HookFunc) error {
	var errs []error
	for _, hook := range hooks {
		if err := hook(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
