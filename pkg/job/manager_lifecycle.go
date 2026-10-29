package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// cronDrainAbortReserve 是排空中止后留给 Cron 任务响应取消、释放集群锁的预算上限。
const cronDrainAbortReserve = time.Second

// Start 启动任务，父 Context 取消时开始统一收敛。
func (m *Manager) Start(parent context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("job manager is already started")
	}
	if m.stopping {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	// 将订阅建立阶段计入生命周期，Stop 不会在 Start 仍创建订阅时提前完成。
	m.workers.Add(1)
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	// Cron 执行上下文不随停机取消，只在排空超时或父 Context 取消时取消。
	cronCtx, abortCron := context.WithCancel(parent)
	m.abortCron = abortCron
	m.mu.Unlock()

	if err := parent.Err(); err != nil {
		m.workers.Done()
		m.beginShutdown()
		return err
	}
	if err := m.subscribeConfig(); err != nil {
		m.workers.Done()
		m.beginShutdown()
		return err
	}
	onceDone, daemonErrors, launched := m.startJobs(ctx, cronCtx)
	m.workers.Done()
	if !launched {
		return nil
	}
	if m.exitWhenDone {
		select {
		case err := <-onceDone:
			m.beginShutdown()
			if err != nil {
				return err
			}
			return ErrCompleted
		case <-m.done:
			return nil
		case <-parent.Done():
			err := parent.Err()
			m.beginShutdown()
			return err
		}
	}
	select {
	case <-m.done:
		return nil
	case err := <-daemonErrors:
		m.beginShutdown()
		return err
	case <-parent.Done():
		err := parent.Err()
		m.beginShutdown()
		return err
	}
}

// Stop 幂等停止任务：Cron 不再触发并排空已准入的调用，单次和常驻任务立即取消。
// 排空在传入 Context 截止前预留 cronDrainAbortReserve 时中止，取消仍在执行的 Cron 调用。
func (m *Manager) Stop(ctx context.Context) error {
	m.beginShutdown()

	drainCtx, cancel := drainContext(ctx)
	defer cancel()
	select {
	case <-m.done:
		return nil
	case <-drainCtx.Done():
	}
	m.abortDrain(ctx)
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drainContext 在调用方截止时间前预留取消收尾预算；无截止时间时与调用方一致。
func drainContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	reserve := min(cronDrainAbortReserve, time.Until(deadline)/2)
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}

// abortDrain 取消仍在执行的 Cron 调用，只记录一次中止日志。
func (m *Manager) abortDrain(ctx context.Context) {
	m.abortOnce.Do(func() {
		m.mu.Lock()
		abort := m.abortCron
		cronStarted := m.cronStarted
		m.mu.Unlock()
		if abort == nil {
			return
		}
		if cronStarted {
			m.log.WithContext(ctx).With("event", "job.cron.drain_aborted").Warn("cron drain timed out; canceling running jobs")
		}
		abort()
	})
}

// beginShutdown 只发出取消信号，避免不响应 Context 的任务反向阻塞 Start。
func (m *Manager) beginShutdown() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.stopping = true
		cancel := m.cancel
		abortCron := m.abortCron
		unsubscribe := m.unsubscribe
		cronStarted := m.cronStarted
		m.mu.Unlock()
		if unsubscribe != nil {
			unsubscribe()
		}
		// 先关闭准入再停调度器：已派发但尚未准入的触发和 Delay 等待都不再执行。
		for _, cronJob := range m.cronJobs {
			cronJob.gate.drain()
		}
		if cancel != nil {
			cancel()
		}
		go func() {
			if cronStarted {
				// 底层调度器停止后不再触发，并等待已经开始的调用返回。
				m.cron.stop()
			}
			m.workers.Wait()
			if abortCron != nil {
				abortCron()
			}
			close(m.done)
		}()
	})
}

// startJobs 在同一状态锁内登记 worker，确保停止开始后不会再出现新的 WaitGroup.Add。
func (m *Manager) startJobs(
	ctx, cronCtx context.Context,
) (onceDone <-chan error, daemonErrors <-chan error, launched bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return nil, nil, false
	}
	if len(m.cronJobs)+len(m.onceJobs)+len(m.daemonJobs) == 0 {
		return nil, nil, false
	}

	for _, cronJob := range m.cronJobs {
		plan := cronJob.scheduleSpec
		if cronJob.resolved.disabled {
			// 启动时禁用也消耗首次启动机会，后续启用不得补跑 immediate。
			plan = nil
		}
		m.cron.schedule(cronCtx, cronJob.name, cronJob.job, plan)
	}
	if len(m.cronJobs) > 0 {
		m.cronStarted = true
		m.cron.start()
	}

	workerCount := len(m.onceJobs) + len(m.daemonJobs)
	if len(m.onceJobs) > 0 {
		// 聚合协程可能还在执行 ErrorHandler；把它计入等待，防止 Stop 提前返回。
		workerCount++
	}
	m.workers.Add(workerCount)
	return m.startOnceJobs(ctx), m.startDaemonJobs(ctx), true
}

// startOnceJobs 并发执行所有单次任务，并在聚合协程中统一处理错误。
func (m *Manager) startOnceJobs(ctx context.Context) <-chan error {
	if len(m.onceJobs) == 0 {
		if !m.exitWhenDone {
			return nil
		}
		done := make(chan error, 1)
		done <- nil
		return done
	}

	var done chan error
	if m.exitWhenDone {
		// 无缓冲交接确定错误出口，避免 Start 因取消退出后把结果留在无人读取的缓冲区。
		done = make(chan error)
	}
	var wait sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, managed := range m.onceJobs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer m.workers.Done()
			jobCtx := withJobName(ctx, managed.name)
			if err := managed.job.Run(jobCtx); err != nil {
				if stoppedByContext(ctx, err) {
					return
				}
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", managed.name, err))
				mu.Unlock()
			}
		}()
	}
	go func() {
		defer m.workers.Done()
		wait.Wait()
		err := errors.Join(errs...)
		if m.exitWhenDone {
			select {
			case done <- err:
				// Start 已接收结果，由返回值处理，不能再通过 ErrorHandler 重复上报。
				return
			case <-ctx.Done():
				// Start 可能已及时返回取消；沿用汇总协程承接剩余真实错误，Stop 仍等待它结束。
			}
		}
		if err != nil {
			m.options.ErrorHandler(ctx, "once", err)
		}
	}()
	return done
}

// startDaemonJobs 启动常驻任务，并把首个非正常退出交给 Manager 触发整体停止。
func (m *Manager) startDaemonJobs(ctx context.Context) <-chan error {
	errorsCh := make(chan error, max(1, len(m.daemonJobs)))
	for _, managed := range m.daemonJobs {
		go func() {
			defer m.workers.Done()
			jobCtx := withJobName(ctx, managed.name)
			err := managed.job.Run(jobCtx)
			if stoppedByContext(ctx, err) {
				return
			}
			if err == nil {
				err = errors.New("daemon exited without cancellation")
			}
			err = fmt.Errorf("daemon job %q: %w", managed.name, err)
			m.options.ErrorHandler(jobCtx, managed.name, err)
			errorsCh <- err
		}()
	}
	return errorsCh
}
