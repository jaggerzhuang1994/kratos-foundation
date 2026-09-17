package job

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronScheduler 只由 Manager 持有，避免出现第二套调度生命周期。
type cronScheduler interface {
	// schedule 注册已经解析的调度计划；nil 保留任务但不安装调度条目。
	schedule(context.Context, string, Task, scheduleSpec)
	// reschedule 发布新的后续计划，保留任务与执行上下文；nil 表示禁用。
	reschedule(string, scheduleSpec)
	// start 启动调度循环。
	start()
	// stop 等待调度循环及已启动任务退出。
	stop()
}

type cron_ struct {
	// mu 保护期望注册表与启停状态。
	mu sync.Mutex
	// desired 按任务名保存的最新调度声明。
	desired map[string]*cronRegistration
	// wake 唤醒控制循环以应用最新声明，重复通知可合并。
	wake chan struct{}
	// stopCh 关闭后请求控制循环停止调度。
	stopCh chan struct{}
	// done 控制循环及已启动 Cron 调用结束的完成信号。
	done chan struct{}
	// started 控制循环是否已经启动。
	started bool
	// stopped 调度器是否已进入停止状态。
	stopped bool

	// log 调度器生命周期日志入口。
	log moduleLog
	// cron 底层周期调度器，由控制循环协调注册变更。
	cron *cron.Cron
	// errorHandler 任务最终失败回调，可能被并发调用。
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
		desired: make(map[string]*cronRegistration), wake: make(chan struct{}, 1), stopCh: make(chan struct{}), done: make(chan struct{}),
		log:          log,
		cron:         cron.New(opt...),
		errorHandler: errorHandler,
	}
}

// cronRegistration 保存一次发布后只读的任务注册。
type cronRegistration struct {
	// job 可复用的任务与执行上下文包装。
	job *cronJob
	// schedule 本次发布的调度计划；nil 表示停用，Next 的可变状态仅由调度协程访问。
	schedule scheduleSpec
}

// schedule 只发布声明并唤醒控制循环，不在 Manager 的状态锁内等待 cron 通道。
func (c *cron_) schedule(ctx context.Context, name string, job Task, schedule scheduleSpec) {
	c.mu.Lock()
	if !c.stopped {
		c.desired[name] = &cronRegistration{job: &cronJob{ctx: withJobName(ctx, name), name: name, job: job, errorHandler: c.errorHandler}, schedule: schedule}
	}
	c.mu.Unlock()
	c.notify()
}

func (c *cron_) reschedule(name string, schedule scheduleSpec) {
	c.mu.Lock()
	if previous := c.desired[name]; previous != nil && !c.stopped {
		c.desired[name] = &cronRegistration{job: previous.job, schedule: schedule}
	}
	c.mu.Unlock()
	c.notify()
}

func (c *cron_) notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *cron_) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.stopped {
		return
	}
	c.started = true
	// 控制循环归调度器所有，stop 发出退出信号并等待其及已启动任务结束。
	go c.run()
}

func (c *cron_) run() {
	defer close(c.done)
	c.log.With("function", "cron.run").Info("starting cron server")
	c.cron.Start()
	applied := make(map[string]*cronRegistration)
	ids := make(map[string]cron.EntryID)
	for {
		select {
		case <-c.stopCh:
			c.log.With("function", "cron.run").Info("stopping cron server")
			<-c.cron.Stop().Done()
			c.log.With("function", "cron.run").Info("cron server stopped")
			return
		case <-c.wake:
			c.mu.Lock()
			snapshot := make(map[string]*cronRegistration, len(c.desired))
			for name, entry := range c.desired {
				snapshot[name] = entry
			}
			c.mu.Unlock()
			// 外部调度调用及 Next/日志均在锁外；整个替换序列只由本协程执行。
			for name, entry := range snapshot {
				if applied[name] == entry {
					continue
				}
				if id, ok := ids[name]; ok {
					c.cron.Remove(id)
					delete(ids, name)
				}
				// 禁用仍保留任务和上下文，重新启用使用新的非立即执行计划。
				if entry.schedule != nil {
					ids[name] = c.cron.Schedule(entry.schedule, entry.job)
				}
				applied[name] = entry
			}
		}
	}
}

func (c *cron_) stop() {
	c.mu.Lock()
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
	started := c.started
	c.mu.Unlock()
	if started {
		<-c.done
	}
}

type cronJob struct {
	// ctx 由 Manager 生命周期控制的任务上下文。
	ctx context.Context
	// name 任务名称，用于错误定位。
	name string
	// job 包含中间件的可执行任务。
	job Task
	// errorHandler 非正常取消错误的统一处理回调。
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
	// log 绑定任务名的调度日志入口。
	log cronLog
	// immediately 是否允许将首次触发提前至当前时刻。
	immediately bool
	// schedule 基础周期计划。
	schedule scheduleSpec
	// immediatelyScheduled 是否已消费首次立即执行机会，仅由调度协程访问。
	immediatelyScheduled bool
}

type scheduleParser struct {
	// log 调度计划的日志入口。
	log cronLog
	// parser 支持可选秒与标准描述符的表达式解析器。
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
