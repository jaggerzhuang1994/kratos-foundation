package job

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// cronConfig 是已合并的执行规则；没有指针，不与外部配置快照共享可变字段。
type cronConfig struct {
	disabled  bool
	schedule  string
	policy    ConcurrentPolicy
	immediate bool
	pending   int
}

// taskCronConfig 只在构造期读取 Task 默认值，避免热更新回调执行业务方法。
func taskCronConfig(def definition) cronConfig {
	result := cronConfig{policy: AllowOverlap, pending: 1}
	if task, ok := def.job.(ScheduleProvider); ok {
		result.schedule = task.Schedule()
	}
	if task, ok := def.job.(ConcurrentPolicyProvider); ok {
		result.policy = task.ConcurrentPolicy()
	}
	if task, ok := def.job.(RunImmediatelyProvider); ok {
		result.immediate = task.RunImmediately()
	}
	if task, ok := def.job.(MaxPendingRunsProvider); ok {
		result.pending = task.MaxPendingRuns()
	}
	if def.schedule != "" {
		result.schedule = def.schedule
	}
	if def.cron.concurrentPolicySet {
		result.policy = def.cron.concurrentPolicy
	}
	if def.cron.runImmediatelySet {
		result.immediate = def.cron.runImmediately
	}
	if def.cron.maxPendingRunsSet {
		result.pending = def.cron.maxPendingRuns
	}
	return result
}

// resolveCronConfig 每次都从注册快照重新合并；删除覆盖字段恢复下层值。
func resolveCronConfig(base cronConfig, override *config_pb.CronJob) (cronConfig, error) {
	if override != nil {
		if override.Disabled != nil {
			base.disabled = *override.Disabled
		}
		if override.Schedule != nil {
			base.schedule = *override.Schedule
		}
		if override.ConcurrentPolicy != nil {
			// 先校验完整枚举值，不能先转 uint8 导致未知值溢出成为有效策略。
			policy := *override.ConcurrentPolicy
			if policy < config_pb.JobConcurrentPolicy_ALLOW_OVERLAP || policy > config_pb.JobConcurrentPolicy_SKIP_IF_RUNNING {
				return cronConfig{}, fmt.Errorf("invalid concurrent policy %d", policy)
			}
			base.policy = ConcurrentPolicy(policy)
		}
		if override.RunImmediately != nil {
			base.immediate = *override.RunImmediately
		}
		if override.MaxPendingRuns != nil {
			base.pending = int(*override.MaxPendingRuns)
		}
	}
	if !base.policy.valid() {
		return cronConfig{}, fmt.Errorf("invalid concurrent policy %d", base.policy)
	}
	if base.pending < -1 || base.pending == int(^uint(0)>>1) {
		return cronConfig{}, fmt.Errorf("max pending runs must be -1 or a non-negative value below max int")
	}
	if base.schedule == "" {
		return cronConfig{}, fmt.Errorf("cron schedule is required")
	}
	return base, nil
}
