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
	// Location Cron 表达式使用的时区，默认 time.Local。
	Location *time.Location
	// TracingEnabled 是否启用任务链路追踪，默认启用。
	TracingEnabled bool
	// MetricsEnabled 是否启用任务指标，默认启用。
	MetricsEnabled bool
	// LoggingEnabled 是否启用任务生命周期日志，默认启用。
	LoggingEnabled bool
	// ErrorHandler 任务最终失败回调；可能并发调用，nil 使用默认错误日志。
	ErrorHandler func(context.Context, string, error)
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
	// runImmediately 首次启动且启用时是否立即执行。
	runImmediately bool
	// runImmediatelySet 是否显式覆盖 Task 的立即执行设置。
	runImmediatelySet bool
	// concurrentPolicySet 是否显式覆盖 Task 的并发策略。
	concurrentPolicySet bool
	// maxPendingRunsSet 是否显式提供等待容量，区分未设置与零。
	maxPendingRunsSet bool
	// concurrentPolicy 本 Manager 内同名周期任务的并发策略。
	concurrentPolicy ConcurrentPolicy
	// maxPendingRuns Delay 等待容量；0 不等待，-1 不限制。
	maxPendingRuns int
	// delayOverflowHandler 等待满额时的同步通知；可能并发调用，nil 仅告警并跳过。
	delayOverflowHandler func(context.Context, DelayOverflow) error
}

// RunImmediately 显式设置首次 Start 且任务启用时是否立即执行，可用 false 覆盖 Task 默认值。
// 运行中重新启用不补跑；进程重启创建新 Manager 后重新判断。
func RunImmediately(enabled bool) CronOption {
	return func(options *cronOptions) {
		options.runImmediately = enabled
		options.runImmediatelySet = true
	}
}

// WithConcurrentPolicy 设置单个周期任务的重叠策略。
func WithConcurrentPolicy(policy ConcurrentPolicy) CronOption {
	return func(options *cronOptions) {
		options.concurrentPolicy = policy
		options.concurrentPolicySet = true
	}
}

// WithMaxPendingRuns 设置 Delay 策略的等待容量，默认 1；0 不保留本地等待名额。
// -1 显式恢复无界等待。容量仅作用于本 Manager 内同一任务。
// AllowOverlap 和 Skip 策略不使用此值。队列满时跳过新触发并记录告警。
func WithMaxPendingRuns(limit int) CronOption {
	return func(options *cronOptions) { options.maxPendingRuns = limit; options.maxPendingRunsSet = true }
}

// DelayOverflow 描述被本进程 Delay 容量限制拒绝的一次 Cron 触发。
type DelayOverflow struct {
	// Name 被拒绝触发的任务名称。
	Name string
	// Policy 拒绝本轮触发时采用的并发策略。
	Policy ConcurrentPolicy
	// MaxPendingRuns 本 Manager 内配置的等待容量，不是当前等待数或全局队列长度。
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
	// middlewares 按登记顺序收集的共享任务中间件。
	middlewares []Middleware
	// options 构造 Manager 时应用的运行策略选项。
	options []ManagerOption
	// definitions 待编译为运行时任务的注册声明。
	definitions []definition
	// exitWhenDone 是否在全部 Once 任务完成后结束应用，仅允许纯 Once 任务。
	exitWhenDone bool
}

type definition struct {
	// name 本 Spec 内唯一的任务名。
	name string
	// kind 周期、单次或常驻任务分类。
	kind kind
	// schedule 周期任务的调度表达式。
	schedule string
	// job 业务提供的任务实现。
	job Task
	// cron 单个周期任务的显式选项。
	cron cronOptions
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
	cron := cronOptions{}
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

// Validate 校验任务身份与生命周期；Cron 参数在 Manager 合并配置之后校验。
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
