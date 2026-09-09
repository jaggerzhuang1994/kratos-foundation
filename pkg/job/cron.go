package job

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// cronScheduler 只由 Manager 持有，避免出现第二套调度生命周期。
type cronScheduler interface {
	// schedule 注册已经解析的调度计划。
	schedule(context.Context, string, Task, scheduleSpec)
	// start 启动调度循环。
	start()
	// stop 等待调度循环及已启动任务退出。
	stop()
}

type cron_ struct {
	log          moduleLog
	cron         *cron.Cron
	errorHandler func(context.Context, string, error)
}

// newCron 只构造调度器而不启动，使启动时机仍由 Manager 生命周期控制。
func newCron(
	log cronLog,
	options managerOptions,
	parser scheduleParserContract,
	logger cronLoggerContract,
) cronScheduler {
	opt := []cron.Option{
		cron.WithParser(parser),
		cron.WithLogger(logger),
	}
	if options.Location != nil {
		opt = append(opt, cron.WithLocation(options.Location))
	}
	errorHandler := options.ErrorHandler
	if errorHandler == nil {
		errorHandler = func(ctx context.Context, name string, err error) {
			log.WithContext(ctx).
				With("job", name, "error", err).
				Error("cron job failed")
		}
	}
	return &cron_{
		log:          log,
		cron:         cron.New(opt...),
		errorHandler: errorHandler,
	}
}

// schedule 注册一个已经完成语法校验的周期任务。
func (c *cron_) schedule(ctx context.Context, name string, job Task, schedule scheduleSpec) {
	c.cron.Schedule(schedule, &cronJob{
		ctx:          withJobName(ctx, name),
		name:         name,
		job:          job,
		errorHandler: c.errorHandler,
	})
}

// start 启动 cron 调度循环。
func (c *cron_) start() {
	c.log.Info("starting cron server")
	c.cron.Start()
}

// stop 等待 cron 调度循环和正在执行的任务结束。
func (c *cron_) stop() {
	c.log.Info("stopping cron server")
	<-c.cron.Stop().Done()
	c.log.Info("cron server stopped")
}

type cronJob struct {
	ctx          context.Context
	name         string
	job          Task
	errorHandler func(context.Context, string, error)
}

// Run 执行一次周期任务，并把非正常取消错误交给统一错误处理器。
func (j *cronJob) Run() {
	if err := j.job.Run(j.ctx); err != nil &&
		!stoppedByContext(j.ctx, err) {
		j.errorHandler(j.ctx, j.name, err)
	}
}

// scheduleSpec 保存解析后的 robfig/cron 调度规则。
type scheduleSpec = cron.Schedule

// scheduleParserContract 解析 cron 表达式，并可为任务增加首次立即执行语义。
type scheduleParserContract interface {
	cron.ScheduleParser
	ParseJob(name, spec string, runImmediately bool) (scheduleSpec, error)
}

type schedule struct {
	log                  cronLog
	immediately          bool
	schedule             scheduleSpec
	immediatelyScheduled bool
}

type scheduleParser struct {
	log    cronLog
	parser cron.ScheduleParser
}

// newScheduleParser 同时支持可选秒字段和 @hourly 等标准描述符，避免业务层选择解析器。
func newScheduleParser(log cronLog) scheduleParserContract {
	return &scheduleParser{
		log: log,
		parser: cron.NewParser(
			cron.SecondOptional |
				cron.Minute |
				cron.Hour |
				cron.Dom |
				cron.Month |
				cron.Dow |
				cron.Descriptor,
		),
	}
}

// Parse 实现 robfig/cron 的解析器契约。
func (p *scheduleParser) Parse(spec string) (scheduleSpec, error) {
	return p.parser.Parse(spec)
}

// ParseJob 为解析错误补充任务名称，并包装首次立即执行语义。
func (p *scheduleParser) ParseJob(name, spec string, runImmediately bool) (scheduleSpec, error) {
	s, err := p.parser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("parse job %q schedule %q: %w", name, spec, err)
	}
	return &schedule{
		log:         p.log.With("job", name),
		immediately: runImmediately,
		schedule:    s,
	}, nil
}

// Next 返回下一次运行时间，并且最多把首次运行提前到当前时刻。
func (s *schedule) Next(now time.Time) time.Time {
	// 立即执行只能消费一次，否则 cron 每次计算都返回 now 并形成忙循环。
	if s.immediately && !s.immediatelyScheduled {
		s.immediatelyScheduled = true
		s.log.Debug("scheduling job immediately")
		return now
	}

	next := s.schedule.Next(now)
	s.log.With("next", next.Format(time.RFC3339), "left", time.Until(next)).Debug("job scheduled")
	return next
}
