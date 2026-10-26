// Package lifecycle 为阻塞式后台运行时提供共享的单次启动与停止协调。
package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrAlreadyStarted 表示 Runner 已经执行过 Run。
var ErrAlreadyStarted = errors.New("runtime already started")

// Runner 协调只允许启动一次的阻塞式运行时：Run 阻塞执行回调，Stop 取消回调并等待 Run 返回。
// 零值不可用，须通过 NewRunner 构造；Run 与 Stop 可并发调用。
type Runner struct {
	// started 原子标记 Run 已被调用，阻止重复启动。
	started atomic.Bool
	// stopped 在首次 Stop 时取消；Run 通过 AfterFunc 把它转为回调 Context 的取消。
	stopped context.Context
	// stop 取消 stopped，可重复调用。
	stop context.CancelFunc
	// done 在 Run 返回时关闭，供 Stop 等待。
	done chan struct{}
}

// NewRunner 创建尚未启动的 Runner。
func NewRunner() *Runner {
	stopped, stop := context.WithCancel(context.Background())
	return &Runner{stopped: stopped, stop: stop, done: make(chan struct{})}
}

// Run 只执行一次 run 并返回其结果；run 收到的 Context 派生自 ctx，在 ctx 取消或 Stop 后取消。
// Stop 先于 Run 时不执行 run 并返回 nil；重复调用返回 ErrAlreadyStarted。
func (r *Runner) Run(ctx context.Context, run func(context.Context) error) error {
	if !r.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	defer close(r.done)
	// Stop 先关闭停止信号再读取 started，因此并发到达的 Stop 必定在这里被观察到。
	if r.stopped.Err() != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	unwatch := context.AfterFunc(r.stopped, cancel)
	defer unwatch()
	return run(runCtx)
}

// Stop 幂等发出停止信号；Run 已开始时等待其返回，等待时间受 ctx 限制。
// Run 已经结束时直接返回 nil，不受同时到期的 ctx 影响。
func (r *Runner) Stop(ctx context.Context) error {
	r.stop()
	if !r.started.Load() {
		return nil
	}
	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
