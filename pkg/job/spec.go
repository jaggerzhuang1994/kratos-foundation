package job

import (
	"context"
	"fmt"
	"time"
)

// kind 区分任务由调度器执行一次、周期执行还是常驻执行。
type kind uint8

const (
	// kindCron 表示周期调度任务。
	kindCron kind = iota + 1
	// kindOnce 表示应用启动后执行一次的任务。
	kindOnce
	// kindDaemon 表示持续运行直到 Context 被取消的任务。
	kindDaemon
)

// managerOptions 保存整个 Spec 解析后的运行策略。
type managerOptions struct {
	Location       *time.Location
	TracingEnabled bool
	MetricsEnabled bool
	LoggingEnabled bool
	ErrorHandler   func(context.Context, string, error)
}

// ManagerOption 调整 Spec 中所有任务共享的运行策略。
type ManagerOption func(*managerOptions)

// WithLocation 设置 cron 表达式使用的时区。
func WithLocation(location *time.Location) ManagerOption {
	return func(options *managerOptions) {
		if location != nil {
			options.Location = location
		}
	}
}

// WithTracing 控制是否为任务记录 tracing。
func WithTracing(enabled bool) ManagerOption {
	return func(options *managerOptions) {
		options.TracingEnabled = enabled
	}
}

// WithMetrics 控制是否为任务记录 metrics。
func WithMetrics(enabled bool) ManagerOption {
	return func(options *managerOptions) {
		options.MetricsEnabled = enabled
	}
}

// WithLogging 控制是否记录任务生命周期日志。
func WithLogging(enabled bool) ManagerOption {
	return func(options *managerOptions) {
		options.LoggingEnabled = enabled
	}
}

// WithErrorHandler 设置任务最终失败时的回调。不同任务可能并发调用该回调，因此实现
// 必须保证并发安全。
func WithErrorHandler(handler func(ctx context.Context, name string, err error)) ManagerOption {
	return func(options *managerOptions) {
		options.ErrorHandler = handler
	}
}

// CronOption 调整单个周期任务。
type CronOption func(*cronOptions)

type cronOptions struct {
	runImmediately       bool
	concurrentPolicy     ConcurrentPolicy
	maxPendingRuns       int
	delayOverflowHandler func(context.Context, DelayOverflow) error
}

// RunImmediately 要求调度器启动后立即执行一次。
func RunImmediately() CronOption {
	return func(options *cronOptions) {
		options.runImmediately = true
	}
}

// WithConcurrentPolicy 设置单个周期任务的重叠策略。
func WithConcurrentPolicy(policy ConcurrentPolicy) CronOption {
	return func(options *cronOptions) {
		options.concurrentPolicy = policy
	}
}

// WithMaxPendingRuns 设置 Delay 策略的等待容量，默认 1；0 不保留本地等待名额。
// -1 显式恢复无界等待。分布式 Delay 限制每进程进入竞争的调用数为 limit+1，非全局队列。
// AllowOverlap 和 Skip 策略不使用此值。队列满时跳过新触发并记录告警。
func WithMaxPendingRuns(limit int) CronOption {
	return func(options *cronOptions) { options.maxPendingRuns = limit }
}

// DelayOverflow 描述被本进程 Delay 容量限制拒绝的一次 Cron 触发。
// MaxPendingRuns 是配置的等待容量，不是全局队列长度。
type DelayOverflow struct {
	Name           string
	Policy         ConcurrentPolicy
	MaxPendingRuns int
}

// WithDelayOverflowHandler 设置单个 Cron 满额时的通知回调；nil 保留默认告警和跳过行为。
// 回调同步执行且可能并发调用，应并发安全、响应 Context 并为外部请求设置超时。
// 返回错误或 panic 会作为本轮错误交给 WithErrorHandler；成功仍跳过本轮。
// 仅有界 Delay 生效；回调不占执行名额，框架不重试、不创建额外 goroutine。
func WithDelayOverflowHandler(handler func(context.Context, DelayOverflow) error) CronOption {
	return func(options *cronOptions) { options.delayOverflowHandler = handler }
}

// Builder 在 Bootstrap 阶段收集任务定义和运行策略。它会修改同一个 Spec，不支持
// 并发调用。
type Builder interface {
	// Middleware 追加所有任务共享的中间件。
	Middleware(...Middleware) Builder
	// Option 追加 Manager 运行策略。
	Option(...ManagerOption) Builder
	// RegisterCron 注册周期任务。
	RegisterCron(name, schedule string, job Task, options ...CronOption) Builder
	// RegisterOnce 注册启动后执行一次的任务。
	RegisterOnce(name string, job Task) Builder
	// RegisterDaemon 注册随 Context 取消而退出的常驻任务。
	RegisterDaemon(name string, job Task) Builder
	// ExitWhenDone 要求所有 Once 任务结束后停止应用。
	ExitWhenDone() Builder
}

// Spec 保存 Manager 构造前收集的任务定义和运行策略。
type Spec struct {
	middlewares  []Middleware
	options      []ManagerOption
	definitions  []definition
	exitWhenDone bool
}

type definition struct {
	name     string
	kind     kind
	schedule string
	job      Task
	cron     cronOptions
}

// NewSpec 返回空的任务定义。
func NewSpec() *Spec { return &Spec{} }

// Middleware 追加所有任务共享的中间件。
func (s *Spec) Middleware(middlewares ...Middleware) Builder {
	for _, middleware := range middlewares {
		if middleware != nil {
			s.middlewares = append(s.middlewares, middleware)
		}
	}
	return s
}

// Option 追加 Manager 运行策略。
func (s *Spec) Option(options ...ManagerOption) Builder {
	for _, option := range options {
		if option != nil {
			s.options = append(s.options, option)
		}
	}
	return s
}

// RegisterCron 向 Spec 注册周期任务。
func (s *Spec) RegisterCron(name, schedule string, job Task, options ...CronOption) Builder {
	cron := cronOptions{concurrentPolicy: AllowOverlap, maxPendingRuns: 1}
	for _, option := range options {
		if option != nil {
			option(&cron)
		}
	}
	s.definitions = append(s.definitions, definition{
		name:     name,
		kind:     kindCron,
		schedule: schedule,
		job:      job,
		cron:     cron,
	})
	return s
}

// RegisterOnce 向 Spec 注册启动后执行一次的任务。
func (s *Spec) RegisterOnce(name string, job Task) Builder {
	s.definitions = append(s.definitions, definition{
		name: name,
		kind: kindOnce,
		job:  job,
	})
	return s
}

// RegisterDaemon 向 Spec 注册常驻任务。
func (s *Spec) RegisterDaemon(name string, job Task) Builder {
	s.definitions = append(s.definitions, definition{
		name: name,
		kind: kindDaemon,
		job:  job,
	})
	return s
}

// ExitWhenDone 要求所有 Once 任务结束后停止应用。
func (s *Spec) ExitWhenDone() Builder {
	s.exitWhenDone = true
	return s
}

// Validate 拒绝不完整、重复或策略不兼容的任务定义。
func (s *Spec) Validate() error {
	names := make(map[string]struct{}, len(s.definitions))
	var once int
	for _, definition := range s.definitions {
		if definition.name == "" {
			return fmt.Errorf("job name is required")
		}
		if definition.job == nil {
			return fmt.Errorf("job %q is nil", definition.name)
		}
		if definition.kind == kindCron && definition.schedule == "" {
			return fmt.Errorf("cron job %q requires a schedule", definition.name)
		}
		if definition.kind == kindCron && (definition.cron.maxPendingRuns < -1 || definition.cron.maxPendingRuns == int(^uint(0)>>1)) {
			return fmt.Errorf("job %q max pending runs must be -1 or a non-negative value below max int", definition.name)
		}
		if definition.kind == kindCron && !definition.cron.concurrentPolicy.valid() {
			return fmt.Errorf(
				"cron job %q has invalid concurrent policy %d",
				definition.name,
				definition.cron.concurrentPolicy,
			)
		}
		if _, ok := names[definition.name]; ok {
			return fmt.Errorf("job %q is already registered", definition.name)
		}
		names[definition.name] = struct{}{}
		if definition.kind == kindOnce {
			once++
		}
	}
	if s.exitWhenDone {
		if once == 0 {
			return fmt.Errorf("ExitWhenDone requires at least one once job")
		}
		for _, definition := range s.definitions {
			if definition.kind != kindOnce {
				return fmt.Errorf("ExitWhenDone only supports once jobs")
			}
		}
	}
	return nil
}

// newManagerOptions 在运行时默认值上应用 Spec 策略，避免构造阶段和执行阶段重复合并。
func newManagerOptions(spec *Spec) managerOptions {
	options := managerOptions{
		Location:       time.Local,
		TracingEnabled: true,
		MetricsEnabled: true,
		LoggingEnabled: true,
	}
	for _, option := range spec.options {
		option(&options)
	}
	return options
}
