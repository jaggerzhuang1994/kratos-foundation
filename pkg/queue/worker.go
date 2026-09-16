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

// WorkerConfig 控制 Worker 的任务处理，构造时固化；零值启用处理并使用默认参数。
// 发布不受 DisableProcessing 影响；配置不支持热更新。
type WorkerConfig struct {
	Name              string                                    // 空值使用 Definition.Queue。
	DisableProcessing bool                                      // 禁用时 Start 等待停止，不领取任务；仍校验配置和依赖。
	Concurrency       int                                       // 默认 1，最大 1024。
	Timeout           time.Duration                             // 默认 30s，包含领取、限流等待和处理耗时。
	MaxAttempts       int                                       // 默认 3；Retry 非 nil 时必须为零，避免两处定义冲突。
	PollInterval      time.Duration                             // 默认 200ms。
	StorageTimeout    time.Duration                             // 默认 5s。
	Lease             time.Duration                             // 默认 max(60s, Timeout + StorageTimeout + 1s)。
	Retry             *RetryPolicy                              // 可选高级退避配置，沿用 Worker 的显式零值语义。
	BeforeHandle      func(context.Context) error               // 解码校验后、处理中间件前等待共享限流器；失败消耗本次领取。
	ClassifyError     func(error) error                         // 将执行错误映射为 Permanent 或普通可重试错误；返回 nil 保留原错误。
	OnFailed          func(context.Context, FailureEvent) error // 仅归档成功后通知，非可靠消息。
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

// workerConfig 固化单队列 Worker 配置，零值使用注释中的默认值。
type workerConfig struct {
	Name           string
	Queue          string
	Concurrency    int           // 默认 1，范围 1..1024。
	PollInterval   time.Duration // 默认 200ms；没有到期任务时的轮询间隔。
	Timeout        time.Duration // 默认 30s，从领取请求开始计算的协作执行窗口，包含领取耗时。
	Lease          time.Duration // 默认 60s，必须大于 Timeout + StorageTimeout。
	StorageTimeout time.Duration // 默认 5s，每个存储操作的最长等待。
	Retry          *RetryPolicy  // nil 使用 3 次领取、500ms 初始退避和 30s 最大退避。
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
	config    workerConfig
	store     Store
	handlers  map[string]taskHandler
	retry     retryPolicy
	log       log.Logger
	telemetry *internaltelemetry.Telemetry
	started   atomic.Bool
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	disabled  bool
	onFailed  func(context.Context, FailureEvent) error
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
