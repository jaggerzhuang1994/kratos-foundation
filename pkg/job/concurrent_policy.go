package job

import (
	"context"
	"fmt"
	"sync"
)

// ConcurrentPolicy 决定本 Manager 内同一周期任务重叠时继续、等待还是跳过。
type ConcurrentPolicy uint8

const (
	// AllowOverlap 允许同一任务的多次调用重叠执行。
	AllowOverlap ConcurrentPolicy = iota
	// DelayIfRunning 在本进程内等待正在运行的调用结束。
	DelayIfRunning
	// SkipIfRunning 在本进程已有同名任务运行时跳过本轮。
	SkipIfRunning
)

func (p ConcurrentPolicy) valid() bool { return p <= SkipIfRunning }

// executionGate 控制任务准入，并在热更新期间保留执行与等待状态。
type executionGate struct {
	// mu 保护准入策略、计数及唤醒信号；任务执行、阻塞等待、日志与业务通知均在锁外。
	mu sync.Mutex
	// disabled 是否拒绝新的调用准入，已排队调用不重新检查。
	disabled bool
	// policy 后续触发使用的并发策略。
	policy ConcurrentPolicy
	// limit Delay 等待容量；负数表示不限制。
	limit int
	// running 已经准入且尚未返回的调用数。
	running int
	// pending 等待执行名额的调用数。
	pending int
	// changed 执行结束时关闭并重建，用于唤醒等待者。
	changed chan struct{}
	// name 受控任务名称。
	name string
	// log 跳过和等待满额事件的日志入口。
	log moduleLog
	// overflow 满额时在锁外同步调用的通知，可为 nil。
	overflow func(context.Context, DelayOverflow) error
}

func newExecutionGate(log moduleLog, name string, config cronConfig, overflow func(context.Context, DelayOverflow) error) *executionGate {
	return &executionGate{disabled: config.disabled, policy: config.policy, limit: config.pending, changed: make(chan struct{}), name: name, log: log, overflow: overflow}
}

func (g *executionGate) update(config cronConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disabled, g.policy, g.limit = config.disabled, config.policy, config.pending
}

func (g *executionGate) middleware(next Handler) Handler {
	return func(ctx context.Context) error {
		admitted, err := g.acquire(ctx)
		if err != nil || !admitted {
			return err
		}
		defer g.release()
		return next(ctx)
	}
}

func (g *executionGate) acquire(ctx context.Context) (bool, error) {
	g.mu.Lock()
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return false, err
	}
	// 禁用与准入共用现有锁，阻止尚未移除的调度条目发起新调用；已排队调用不重查开关。
	if g.disabled {
		g.mu.Unlock()
		return false, nil
	}
	if g.policy == AllowOverlap || (g.running == 0 && g.pending == 0) {
		g.running++
		g.mu.Unlock()
		return true, nil
	}
	if g.policy == SkipIfRunning {
		g.mu.Unlock()
		g.log.WithContext(ctx).With("function", "executionGate.acquire", "job", g.name).Warn("job skipped")
		return false, nil
	}
	if g.limit >= 0 && g.pending >= g.limit {
		event := DelayOverflow{Name: g.name, Policy: g.policy, MaxPendingRuns: g.limit}
		g.mu.Unlock()
		g.log.WithContext(ctx).With("function", "executionGate.acquire", "job", g.name, "max_pending_runs", event.MaxPendingRuns).Warn("Skipped job trigger because the pending-run queue is full")
		if g.overflow != nil {
			return false, handleDelayOverflow(ctx, event, g.overflow)
		}
		return false, nil
	}
	g.pending++
	// 已经进入等待的调用保留串行等待语义；后续新触发才使用热更新后的策略。
	for {
		if err := ctx.Err(); err != nil {
			g.pending--
			g.mu.Unlock()
			return false, err
		}
		if g.running == 0 {
			g.pending--
			g.running++
			g.mu.Unlock()
			return true, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		g.mu.Lock()
	}
}

func (g *executionGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running--
	close(g.changed)
	g.changed = make(chan struct{})
}

// handleDelayOverflow 隔离业务回调 panic，错误交给 Cron 的最终错误入口。
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
