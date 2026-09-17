package job

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func (m *Manager) validateConfigNames(config *config_pb.Job) error {
	if config == nil {
		return nil
	}
	names := make(map[string]bool, len(m.cronJobs))
	for i := range m.cronJobs {
		names[m.cronJobs[i].name] = true
	}
	for name := range config.Cron {
		if !names[name] {
			return fmt.Errorf("job config references unregistered cron task %q", name)
		}
	}
	return nil
}

// subscribeConfig 由 Start 建立订阅，Stop 取消；无构造期资源或额外 cleanup。
func (m *Manager) subscribeConfig() error {
	if m.config == nil || len(m.cronJobs) == 0 {
		return nil
	}
	var current config_pb.Job
	if err := m.config.Load("job", &current, &config_pb.Job{}); err != nil {
		return fmt.Errorf("load job config: %w", err)
	}
	if err := m.applyConfig(&current); err != nil {
		return err
	}
	cancel, err := m.config.Subscribe("job", &config_pb.Job{}, func(_ string, value any, err error) {
		if err == nil {
			err = m.applyConfig(value.(*config_pb.Job))
		}
		if err != nil {
			m.log.With("function", "Manager.subscribeConfig", "event", "job.config.rejected", "error", err).Error("job configuration rejected; keeping previous rules")
		}
	}, &config_pb.Job{})
	if err != nil {
		return fmt.Errorf("subscribe job config: %w", err)
	}
	m.mu.Lock()
	stopped := m.stopping
	if !stopped {
		m.unsubscribe = cancel
	}
	m.mu.Unlock()
	if stopped {
		cancel()
	}
	return nil
}

// applyConfig 先完整解析，再在生命周期锁内发布规则；任一任务无效都不改变旧快照。
// 热更新只替换未来调度。运行计数与已排队调用属于 gate，不随配置替换。
func (m *Manager) applyConfig(config *config_pb.Job) error {
	if err := m.validateConfigNames(config); err != nil {
		return err
	}
	resolved := make([]cronConfig, len(m.cronJobs))
	plans := make([]scheduleSpec, len(m.cronJobs))
	for i := range m.cronJobs {
		name, base := m.cronJobs[i].name, m.cronJobs[i].base
		var override *config_pb.CronJob
		if config != nil {
			override = config.Cron[name]
		}
		next, err := resolveCronConfig(base, override)
		if err != nil {
			return fmt.Errorf("cron job %q: %w", name, err)
		}
		// 不在这里消费或修改旧 schedule 的立即执行状态。
		plan, err := m.parser.ParseJob(name, next.schedule, false)
		if err != nil {
			return fmt.Errorf("cron job %q: %w", name, err)
		}
		resolved[i], plans[i] = next, plan
	}
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return nil
	}
	changed := false
	for i := range m.cronJobs {
		task := &m.cronJobs[i]
		next := resolved[i]
		if task.resolved == next {
			continue
		}
		changed = true
		task.gate.update(next)
		if m.cronStarted {
			if task.resolved.schedule != next.schedule || task.resolved.disabled != next.disabled {
				plan := plans[i]
				if next.disabled {
					plan = nil
				}
				m.cron.reschedule(task.name, plan)
			}
		} else {
			// 尚未启动时保留最新 runImmediately；运行期修改绝不补触发。
			task.scheduleSpec = &schedule{log: m.log, immediately: next.immediate, schedule: plans[i]}
		}
		task.resolved = next
		for registrationIndex := range m.registrations {
			registration := &m.registrations[registrationIndex]
			if registration.kind == "cron" && registration.name == task.name {
				registration.schedule = next.schedule
				registration.enabled = !next.disabled
				break
			}
		}
	}
	m.mu.Unlock()
	if changed {
		m.log.With("function", "Manager.applyConfig", "event", "job.config.applied").Info("job configuration applied")
	}
	return nil
}
