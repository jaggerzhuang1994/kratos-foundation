package job

import (
	"context"
	"fmt"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Manager 持有 Spec 中任务启动的全部 goroutine，并统一处理停止和等待。
type Manager struct {
	// config 可选配置管理器；nil 时仅使用代码声明。
	config config.Manager
	// unsubscribe 停止配置热更新订阅的取消函数。
	unsubscribe func()
	// parser Cron 表达式解析器。
	parser scheduleParserContract
	// log 任务运行日志入口。
	log moduleLog
	// exitWhenDone 是否在全部单次任务结束后退出。
	exitWhenDone bool
	// options 构造时解析的共享运行策略。
	options managerOptions
	// cron 由 Manager 管理生命周期的周期调度器。
	cron cronScheduler
	// cronJobs 周期任务及其基础、当前配置。
	cronJobs []scheduledJob
	// onceJobs 启动后执行一次的任务集合。
	onceJobs []*managedJob
	// daemonJobs 随运行上下文取消而退出的常驻任务集合。
	daemonJobs []*managedJob

	// mu 保护启停状态与周期配置更新。
	mu sync.Mutex
	// cancel 停止时取消所有任务的运行上下文。
	cancel context.CancelFunc
	// cronStarted 周期调度器是否已实际启动。
	cronStarted bool
	// started 是否已经执行首次 Start，防止重复启动。
	started bool
	// stopping 是否正在停止，阻止新的启动或配置更新。
	stopping bool
	// workers 等待启动登记、单次/常驻任务及单次错误汇总协程退出。
	workers sync.WaitGroup
	// stopOnce 确保停止收尾仅执行一次。
	stopOnce sync.Once
	// done 所有受管任务和调度器结束的完成信号。
	done chan struct{}
}

type scheduledJob struct {
	// managedJob 已应用中间件的周期任务。
	*managedJob
	// scheduleSpec 启动时使用的解析计划；运行中后续计划由 cron.reschedule 更新。
	scheduleSpec
	// base 代码声明形成的基础配置，配置删除时回退至此。
	base cronConfig
	// resolved 合并动态覆盖后生效的完整配置。
	resolved cronConfig
	// gate 热更新期间保留计数的并发准入控制。
	gate *executionGate
}

// NewManager 把任务定义解析为一个运行时，调度器和观测中间件不会作为独立生命周期暴露。
// configManager 为 nil 时只使用注册和 Task 声明；否则读取 job.cron，并在 Start/Stop 管理订阅。
func NewManager(
	logger foundationlog.Logger,
	spec *Spec,
	tracingProvider foundationtracing.Provider,
	metricsProvider foundationmetrics.Provider,
	configManager config.Manager,
) (*Manager, error) {
	// 先验证 Spec，避免非法任务触发日志、指标等无意义的构造副作用。
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	var initial config_pb.Job
	if configManager != nil {
		if err := configManager.Load("job", &initial, &config_pb.Job{}); err != nil {
			return nil, fmt.Errorf("load job config: %w", err)
		}
	}
	options := newManagerOptions(spec)
	jobLogger := newJobLog(logger, options)
	middlewares, err := newMiddlewares(
		jobLogger,
		options,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		return nil, err
	}
	cronLogger := newCronLog(logger, options)
	parser := newScheduleParser(cronLogger)
	scheduler := newCron(
		cronLogger,
		options,
		parser,
		newCronLogger(cronLogger),
	)
	manager, err := newManager(
		jobLogger,
		middlewares,
		spec,
		options,
		scheduler,
		parser,
		&initial,
	)
	if err != nil {
		return nil, err
	}
	manager.config = configManager
	return manager, nil
}

// newManager 把已校验 Spec 编译为周期、单次和常驻三类可执行任务。
func newManager(
	log moduleLog,
	middlewares middlewareChain,
	spec *Spec,
	options managerOptions,
	cron cronScheduler,
	parser scheduleParserContract,
	configuration *config_pb.Job,
) (*Manager, error) {
	if options.ErrorHandler == nil {
		options.ErrorHandler = func(ctx context.Context, name string, err error) {
			log.WithContext(ctx).With("job", name, "error", err).Error("job failed")
		}
	}

	manager := &Manager{
		parser: parser, log: log,
		exitWhenDone: spec.exitWhenDone,
		options:      options,
		cron:         cron,
		done:         make(chan struct{}),
	}

	baseMiddlewares := append(middlewareChain(nil), middlewares...)
	baseMiddlewares = append(baseMiddlewares, spec.middlewares...)
	for _, definition := range spec.definitions {
		switch definition.kind {
		case kindCron:
			base := taskCronConfig(definition)
			var override *config_pb.CronJob
			if configuration != nil {
				override = configuration.Cron[definition.name]
			}
			resolved, err := resolveCronConfig(base, override)
			if err != nil {
				return nil, fmt.Errorf("cron job %q: %w", definition.name, err)
			}
			schedule, err := parser.ParseJob(definition.name, resolved.schedule, resolved.immediate)
			if err != nil {
				return nil, fmt.Errorf("parse cron job %q: %w", definition.name, err)
			}
			gate := newExecutionGate(log, definition.name, resolved, definition.cron.delayOverflowHandler)
			jobMiddlewares := append([]Middleware{gate.middleware}, baseMiddlewares...)
			manager.cronJobs = append(manager.cronJobs, scheduledJob{
				managedJob:   newManagedJob(definition.name, definition.job, jobMiddlewares),
				scheduleSpec: schedule, base: base, resolved: resolved, gate: gate,
			})
		case kindOnce:
			manager.onceJobs = append(
				manager.onceJobs,
				newManagedJob(definition.name, definition.job, baseMiddlewares),
			)
		case kindDaemon:
			manager.daemonJobs = append(
				manager.daemonJobs,
				newManagedJob(definition.name, definition.job, baseMiddlewares),
			)
		}
	}
	if err := manager.validateConfigNames(configuration); err != nil {
		return nil, err
	}
	return manager, nil
}

// HasJobs 表示 Spec 中是否存在需要运行的任务。
func (m *Manager) HasJobs() bool {
	return len(m.cronJobs)+len(m.onceJobs)+len(m.daemonJobs) > 0
}

// IsOneShot 表示任务完成后是否应结束进程。
func (m *Manager) IsOneShot() bool {
	return m.exitWhenDone
}
