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
	config       config.Manager
	unsubscribe  func()
	parser       scheduleParserContract
	log          moduleLog
	exitWhenDone bool
	options      managerOptions
	cron         cronScheduler
	cronJobs     []scheduledJob
	onceJobs     []*managedJob
	daemonJobs   []*managedJob

	mu          sync.Mutex
	cancel      context.CancelFunc
	cronStarted bool
	started     bool
	stopping    bool
	workers     sync.WaitGroup
	stopOnce    sync.Once
	done        chan struct{}
}

type scheduledJob struct {
	*managedJob
	scheduleSpec
	base     cronConfig
	resolved cronConfig
	gate     *executionGate
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
