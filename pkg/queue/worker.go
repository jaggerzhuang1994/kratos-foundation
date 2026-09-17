package queue

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue/internal/telemetry"
	"golang.org/x/sync/errgroup"
)

// WorkerConfig 控制任务处理，构造时固化，不支持热更新；零值启用处理并使用默认参数。
// 回调由并发消费循环共享，须并发安全；带 Context 的回调必须响应取消。
type WorkerConfig struct {
	// Name 为 Worker 的观测名称；空值使用 Definition.Queue。
	Name string
	// DisableProcessing 禁止领取任务，Start 仅等待停止；仍校验配置和依赖，不影响发布。
	DisableProcessing bool
	// Concurrency 为并发领取循环数，默认 1，最大 1024。
	Concurrency int
	// Timeout 为从领取开始的协作执行预算，零值默认 30s，负值无效。
	// 包含解码、校验、限流和处理，不含确认/归档；不能强制终止不响应取消的 Handler。
	Timeout time.Duration
	// MaxAttempts 为总领取次数上限，零值默认 3，有效范围 1–1000；Retry 非 nil 时必须为零。
	MaxAttempts int
	// PollInterval 为未领取到任务后的等待间隔，零值默认 200ms，负值无效。
	PollInterval time.Duration
	// StorageTimeout 为单次存储操作及失败通知的协作超时，零值默认 5s，负值无效。
	StorageTimeout time.Duration
	// Lease 为领取租约时长，至少 1ms 且须大于 Timeout + StorageTimeout，不自动续租。
	// 零值默认 max(60s, Timeout + StorageTimeout + 1s)。
	Lease time.Duration
	// Retry 可选；nil 使用 MaxAttempts 和 500ms/30s 退避，非 nil 时不补齐其中的零值。
	Retry *RetryPolicy
	// BeforeHandle 在解码校验后、处理中间件前执行，可用于等待限流器；失败消耗本次领取。
	BeforeHandle func(context.Context) error
	// ClassifyError 分类 BeforeHandle/Handler 的非永久错误；返回 nil 保留原错误。
	// 解码、校验失败和 panic 不经过分类器；执行上下文超时或取消仍优先于分类结果。
	ClassifyError func(error) error
	// OnFailed 在归档成功后同步通知，独立使用 StorageTimeout 预算；失败不重试通知或回滚归档。
	OnFailed func(context.Context, FailureEvent) error
}

func (c WorkerConfig) workerConfig(queue string) (workerConfig, error) {
	if c.Name == "" {
		c.Name = queue
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.StorageTimeout == 0 {
		c.StorageTimeout = 5 * time.Second
	}
	if c.Lease == 0 {
		// 先验证加法边界，避免极大时长溢出后生成过短租约。
		if c.Timeout < 0 || c.StorageTimeout < 0 || c.StorageTimeout > math.MaxInt64-time.Second || c.Timeout > math.MaxInt64-time.Second-c.StorageTimeout {
			return workerConfig{}, errors.New("invalid queue processing execution window")
		}
		c.Lease = max(60*time.Second, c.Timeout+c.StorageTimeout+time.Second)
	}
	if c.Retry != nil && c.MaxAttempts != 0 {
		return workerConfig{}, errors.New("queue processing max attempts conflicts with retry policy")
	}
	if c.Retry == nil {
		if c.MaxAttempts == 0 {
			c.MaxAttempts = defaultMaxAttempts
		}
		c.Retry = &RetryPolicy{MaxAttempts: c.MaxAttempts, MinBackoff: defaultMinBackoff, MaxBackoff: defaultMaxBackoff}
	}
	return workerConfig{Name: c.Name, Queue: queue, Concurrency: c.Concurrency, PollInterval: c.PollInterval, Timeout: c.Timeout, StorageTimeout: c.StorageTimeout, Lease: c.Lease, Retry: c.Retry}, nil
}

// workerConfig 保存供底层消费循环解析和校验的参数。
type workerConfig struct {
	// Name 为日志和指标中的 Worker 名称。
	Name string
	// Queue 为当前 Worker 消费的逻辑队列名称。
	Queue string
	// Concurrency 为并发领取循环数，默认 1，范围 1–1024。
	Concurrency int
	// PollInterval 为没有到期任务时的轮询间隔，默认 200ms。
	PollInterval time.Duration
	// Timeout 为从领取开始计算的协作执行窗口，默认 30s。
	Timeout time.Duration
	// Lease 为领取租约时长，默认 60s，须大于 Timeout + StorageTimeout。
	Lease time.Duration
	// StorageTimeout 为单次存储操作及失败通知的协作超时，零值默认 5s，负值无效。
	StorageTimeout time.Duration
	// Retry 为重试策略；nil 使用 3 次领取、500ms 初始退避和 30s 最大退避。
	Retry *RetryPolicy
}

func (config workerConfig) resolve() (workerConfig, error) {
	config.Name, config.Queue = strings.TrimSpace(config.Name), strings.TrimSpace(config.Queue)
	if config.Name == "" || config.Queue == "" {
		return workerConfig{}, errors.New("queue worker name and queue are required")
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.PollInterval == 0 {
		config.PollInterval = 200 * time.Millisecond
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Lease == 0 {
		config.Lease = 60 * time.Second
	}
	if config.StorageTimeout == 0 {
		config.StorageTimeout = 5 * time.Second
	}
	if config.Concurrency < 1 || config.Concurrency > 1024 || config.PollInterval < 0 || config.Timeout < 0 || config.StorageTimeout < 0 || config.Lease < time.Millisecond || config.Lease <= config.Timeout || config.Lease-config.Timeout <= config.StorageTimeout {
		return workerConfig{}, errors.New("invalid queue worker concurrency or timing configuration")
	}
	return config, nil
}

// taskHandler 是底层原始任务处理器；依赖由业务注入，必须响应 Context 取消并自行保证业务幂等。
type taskHandler func(context.Context, *Task) error

// Worker 是类型化消费运行时，管理领取、租约和重试，由 Queue.Worker 创建。
// 实现应用 Runtime 的 Start/Stop 契约，消息类型使不同 Worker 可由 Wire 区分。
// 一个实例只启动一次，关闭由应用组装层负责，Store 的连接仍由原拥有者释放。
type Worker[T any] struct {
	// config 保存已解析的消费参数。
	config workerConfig
	// store 为并发领取循环共享的存储后端。
	store Store
	// handlers 按完整消息版本匹配处理器，构造时复制后只读。
	handlers map[string]taskHandler
	// retry 保存已校验的领取次数与退避策略。
	retry retryPolicy
	// log 为队列模块日志入口。
	log log.Logger
	// telemetry 记录处理、重试和归档的追踪及指标。
	telemetry *internaltelemetry.Telemetry
	// started 原子标记是否已启动，阻止重复调用 Start。
	started atomic.Bool
	// stop 关闭后通知协调循环取消运行上下文。
	stop chan struct{}
	// done 在 Start 退出时关闭，供 Stop 等待运行结束。
	done chan struct{}
	// stopOnce 保证停止信号只关闭一次。
	stopOnce sync.Once
	// disabled 在构造期固化；为 true 时不领取任务。
	disabled bool
	// onFailed 为归档成功后的可选通知回调，不保证可靠投递。
	onFailed func(context.Context, FailureEvent) error
}

// newWorker 复制 Handler 表并校验执行窗口，构造阶段不调用 Store。
func newWorker[T any](config workerConfig, store Store, handlers map[string]taskHandler, observability Observability) (*Worker[T], error) {
	config, err := config.resolve()
	if err != nil {
		return nil, err
	}
	if len(handlers) == 0 {
		return nil, errors.New("queue worker handlers are empty")
	}
	for name, handler := range handlers {
		if name == "" || strings.TrimSpace(name) != name || handler == nil {
			return nil, errors.New("queue worker handler is invalid")
		}
	}
	retry, err := resolveRetryPolicy(config.Retry)
	if err != nil {
		return nil, err
	}
	telemetry, err := internaltelemetry.New(observability.Tracing, observability.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create queue worker telemetry: %w", err)
	}
	return &Worker[T]{config: config, store: store, handlers: maps.Clone(handlers), retry: retry, log: observability.Logger.WithModule("queue"), telemetry: telemetry, stop: make(chan struct{}), done: make(chan struct{})}, nil
}

// Start 阻塞运行所有领取循环；存储故障取消同实例其他循环，等待全部退出后返回错误。
func (w *Worker[T]) Start(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("queue worker already started")
	}
	defer close(w.done)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	coordinatorDone := make(chan struct{})
	// 唯一协调 goroutine 合并 Stop 与父 Context；退出前始终等待它结束。
	go func() {
		defer close(coordinatorDone)
		select {
		case <-w.stop:
			cancel()
		case <-runCtx.Done():
		}
	}()
	defer func() { cancel(); <-coordinatorDone }()
	select {
	case <-w.stop:
		return nil
	default:
	}
	if w.disabled {
		// 复用已有停止信号和协调 goroutine，禁用消费不构造伪 Store，也不进入领取循环。
		w.log.WithContext(ctx).Debugw("function", "Start", "event", "consumer.disabled", "queue", w.config.Queue)
		<-runCtx.Done()
		return nil
	}
	group, groupCtx := errgroup.WithContext(runCtx)
	for range w.config.Concurrency {
		group.Go(func() error { return w.run(groupCtx) })
	}
	err := group.Wait()
	if err != nil {
		w.telemetry.RecordRuntimeFailure(ctx, w.config.Queue, w.config.Name)
		w.log.WithContext(ctx).Errorw("event", "storage.failed", "queue", w.config.Queue, "worker", w.config.Name, "error", err)
		return err
	}
	return nil
}

// Stop 幂等发出停止信号并等待 Handler 与存储操作退出；等待受 ctx 控制。
func (w *Worker[T]) Stop(ctx context.Context) error {
	w.stopOnce.Do(func() { close(w.stop) })
	if !w.started.Load() {
		return nil
	}
	select {
	case <-w.done:
		return nil
	default:
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker[T]) run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		now := time.Now()
		operationCtx, cancel := context.WithTimeout(ctx, w.config.StorageTimeout)
		reservation, err := w.store.Reserve(operationCtx, now.UTC(), w.config.Lease)
		cancel()
		if err != nil {
			if ctx.Err() != nil && errorOnly(err, ctx.Err()) {
				return nil
			}
			return fmt.Errorf("reserve task: %w", err)
		}
		if reservation == nil {
			if err := waitBackoff(ctx, w.config.PollInterval); err != nil {
				return nil
			}
			continue
		}
		if err := w.execute(ctx, reservation, now); err != nil {
			if errorOnly(err, ErrLeaseLost) {
				w.log.WithContext(ctx).Warnw("event", "lease.lost", "queue", w.config.Queue, "task.id", reservation.Task.ID)
				continue
			}
			if ctx.Err() != nil && errorOnly(err, ctx.Err()) {
				return nil
			}
			return err
		}
	}
}

// errorOnly 仅当每个错误叶子均表示指定原因时才允许恢复，避免吞掉聚合存储故障。
func errorOnly(err, target error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		found := false
		for _, child := range joined.Unwrap() {
			if child != nil {
				found = true
				if !errorOnly(child, target) {
					return false
				}
			}
		}
		return found
	}
	if child := errors.Unwrap(err); child != nil {
		return errorOnly(child, target)
	}
	return errors.Is(err, target)
}
