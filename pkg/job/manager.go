package job

import (
	"context"
	"fmt"
	"sync"

	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// Manager 持有 Spec 中任务启动的全部 goroutine，并统一处理停止和等待。
type Manager struct {
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
}

// NewManager 把任务定义解析为一个运行时，调度器和观测中间件不会作为独立生命周期暴露。
// coordinator 仅在使用分布式并发策略时必需，其他策略可传 nil。
func NewManager(
	logger foundationlog.Logger,
	spec *Spec,
	tracingProvider foundationtracing.Provider,
	metricsProvider foundationmetrics.Provider,
	coordinator ConcurrencyCoordinator,
) (*Manager, error) {
	// 先验证 Spec，避免非法任务触发日志、指标等无意义的构造副作用。
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	// 分布式策略必须显式注入协调器，不能因漏配而退化为进程内执行。
	for _, definition := range spec.definitions {
		if definition.kind == kindCron && definition.cron.concurrentPolicy.distributed() && coordinator == nil {
			return nil, fmt.Errorf("cron job %q requires a concurrency coordinator", definition.name)
		}
	}
	options := newManagerOptions(spec)
	jobLogger, err := newJobLog(logger, options)
	if err != nil {
		return nil, fmt.Errorf("configure job logger: %w", err)
	}
	middlewares, err := newMiddlewares(
		jobLogger,
		options,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		return nil, err
	}
	cronLogger, err := newCronLog(logger, options)
	if err != nil {
		return nil, fmt.Errorf("configure cron logger: %w", err)
	}
	parser := newScheduleParser(cronLogger)
	scheduler := newCron(
		cronLogger,
		options,
		parser,
		newCronLogger(cronLogger),
	)
	return newManager(
		jobLogger,
		middlewares,
		spec,
		options,
		scheduler,
		parser,
		coordinator,
	)
}

// newManager 把已校验 Spec 编译为周期、单次和常驻三类可执行任务。
func newManager(
	log moduleLog,
	middlewares middlewareChain,
	spec *Spec,
	options managerOptions,
	cron cronScheduler,
	parser scheduleParserContract,
	coordinator ConcurrencyCoordinator,
) (*Manager, error) {
	if options.ErrorHandler == nil {
		options.ErrorHandler = func(_ context.Context, name string, err error) {
			log.With("job", name, "error", err).Error("job failed")
		}
	}

	manager := &Manager{
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
			schedule, err := parser.ParseJob(
				definition.name,
				definition.schedule,
				definition.cron.runImmediately,
			)
			if err != nil {
				return nil, fmt.Errorf("parse cron job %q: %w", definition.name, err)
			}
			jobMiddlewares := append([]Middleware{
				concurrentMiddleware(
					log,
					definition.cron.concurrentPolicy,
					coordinator,
					definition.name,
					definition.cron.maxPendingRuns,
					definition.cron.delayOverflowHandler,
				),
			}, baseMiddlewares...)
			manager.cronJobs = append(manager.cronJobs, scheduledJob{
				managedJob:   newManagedJob(definition.name, definition.job, jobMiddlewares),
				scheduleSpec: schedule,
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
