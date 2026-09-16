package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

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

	onceDone, daemonErrors, launched := m.startJobs(ctx)
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

// Stop 幂等取消任务，并以传入 Context 限制等待时间。
func (m *Manager) Stop(ctx context.Context) error {
	m.beginShutdown()

	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// beginShutdown 只发出取消信号，避免不响应 Context 的任务反向阻塞 Start。
func (m *Manager) beginShutdown() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.stopping = true
		cancel := m.cancel
		unsubscribe := m.unsubscribe
		cronStarted := m.cronStarted
		m.mu.Unlock()
		if unsubscribe != nil {
			unsubscribe()
		}
		if cancel != nil {
			cancel()
		}
		go func() {
			if cronStarted {
				m.cron.stop()
			}
			m.workers.Wait()
			close(m.done)
		}()
	})
}

// startJobs 在同一状态锁内登记 worker，确保停止开始后不会再出现新的 WaitGroup.Add。
func (m *Manager) startJobs(
	ctx context.Context,
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
		m.cron.schedule(ctx, cronJob.name, cronJob.job, cronJob.scheduleSpec)
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
		done = make(chan error, 1)
	}
	var wait sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, managed := range m.onceJobs {
		managed := managed
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
			done <- err
			return
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
		managed := managed
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
