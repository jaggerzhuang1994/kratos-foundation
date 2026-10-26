package app

import (
	"context"
	"errors"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/errtree"
)

// errAppStopping 表示启动阶段观察到停止请求；它是停机信号而不是故障，不计入最终错误。
var errAppStopping = errors.New("application is stopping")

// Stop 请求停止：撤销就绪、执行一次 BeforeStop 并注销服务后返回，不等待 Runtime 退出。
// 调用方应等待 Run 返回后再执行资源清理；重复调用共享第一次停止前钩子的结果。
func (a *App) Stop() error {
	a.requestStop()
	return a.beginStop()
}

// requestStop 只在首次停止请求时冻结超时，避免热更新改变正在进行的停机预算。
func (a *App) requestStop() {
	first := false
	a.stopOnce.Do(func() {
		a.stopTime = a.stopPolicy.current()
		// 先发布停机状态，再广播解除 Ready 等待并取消启动钩子与注册的 Context。
		a.stopping.Store(true)
		close(a.stoppingSignal)
		a.cancelRun()
		first = true
	})
	if first {
		a.logEvent(a.parent, kratoslog.LevelInfo, "app.stopping", "application stopping", "stop_timeout", a.stopTime)
	}
}

// isStopping 报告应用是否已经进入停止阶段。
func (a *App) isStopping() bool {
	return a.stopping.Load()
}

// stopTimeout 请求停止并返回首次停止时冻结的超时。
func (a *App) stopTimeout() time.Duration {
	a.requestStop()
	return a.stopTime
}

// beginStop 只执行一次停止前钩子与服务注销；并发调用等待同一次执行完成。
func (a *App) beginStop() error {
	a.beginStopOnce.Do(func() {
		a.beforeStopErr = runStopHooks(a.base, a.beforeStop)
		a.deregister()
	})
	return a.beforeStopErr
}

// recordFailure 按发生顺序保存真实故障；nil 与停机信号 errAppStopping 不计入。
func (a *App) recordFailure(err error) {
	if err == nil || errors.Is(err, errAppStopping) {
		return
	}
	a.mu.Lock()
	a.failures = append(a.failures, err)
	a.mu.Unlock()
}

// startupCallbackError 将统一停机取消的启动回调 Context 错误归类为停机信号。
func (a *App) startupCallbackError(ctx context.Context, err error) error {
	// 取消与真实故障可能被 errors.Join 合并；只归一化纯取消，避免吞掉注册或钩子失败。
	if a.isStopping() && ctx.Err() == context.Canceled && errtree.Only(err, context.Canceled) {
		return errAppStopping
	}
	return err
}

// runAfterStart 执行启动后钩子，并在并发停止请求出现时立即收敛；全部成功才发布 Ready。
func (a *App) runAfterStart(ctx context.Context) error {
	for _, hook := range a.afterStart {
		if a.isStopping() {
			return errAppStopping
		}
		if err := hook(ctx); err != nil {
			return a.startupCallbackError(ctx, err)
		}
	}
	if a.isStopping() {
		return errAppStopping
	}
	// 原子状态先发布，再关闭 channel；等待者解除阻塞后即可观察完整 ready 状态。
	if a.ready.CompareAndSwap(false, true) {
		close(a.readySignal)
		a.logEvent(ctx, kratoslog.LevelInfo, "app.ready", "application ready")
	}
	return nil
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
