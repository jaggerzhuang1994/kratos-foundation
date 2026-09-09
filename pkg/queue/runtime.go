package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue/internal/telemetry"
)

// RuntimeConfig 描述一个业务消费者及其统一重试和死信策略。
type RuntimeConfig struct {
	Name                  string
	Destination           string
	Retry                 *RetryPolicy
	DeadLetter            Producer
	DeadLetterDestination string
}

// ConsumerRuntime 将外部 Consumer 绑定到业务 Handler，并实现统一生命周期、重试、死信与观测策略。
type ConsumerRuntime struct {
	config    RuntimeConfig
	consumer  Consumer
	handler   Handler
	log       log.Logger
	telemetry *internaltelemetry.Telemetry
	retry     retryPolicy

	started  atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewConsumerRuntime 校验配置并创建只能启动一次的受管消费者运行时。
func NewConsumerRuntime(
	config RuntimeConfig,
	consumer Consumer,
	handler Handler,
	observability Observability,
) (*ConsumerRuntime, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return nil, errors.New("queue consumer name is empty")
	}
	config.Destination = strings.TrimSpace(config.Destination)
	if config.Destination == "" {
		return nil, fmt.Errorf("queue consumer %q destination is empty", config.Name)
	}
	if handler == nil {
		return nil, fmt.Errorf("queue consumer %q handler is nil", config.Name)
	}
	config.DeadLetterDestination = strings.TrimSpace(config.DeadLetterDestination)
	if config.DeadLetter == nil && config.DeadLetterDestination != "" {
		return nil, fmt.Errorf("queue consumer %q dead-letter producer is nil", config.Name)
	}
	if config.DeadLetter != nil && config.DeadLetterDestination == "" {
		return nil, fmt.Errorf("queue consumer %q dead-letter destination is empty", config.Name)
	}
	retry, err := resolveRetryPolicy(config.Retry)
	if err != nil {
		return nil, fmt.Errorf("queue consumer %q: %w", config.Name, err)
	}
	telemetry, err := internaltelemetry.New(observability.Tracing, observability.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create queue consumer telemetry: %w", err)
	}
	return newConsumerRuntime(
		config,
		consumer,
		handler,
		observability.Logger,
		telemetry,
		retry,
	), nil
}

func newConsumerRuntime(
	config RuntimeConfig,
	consumer Consumer,
	handler Handler,
	logger log.Logger,
	telemetry *internaltelemetry.Telemetry,
	retry retryPolicy,
) *ConsumerRuntime {
	return &ConsumerRuntime{
		config:    config,
		consumer:  consumer,
		handler:   handler,
		log:       logger,
		telemetry: telemetry,
		retry:     retry,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start 启动消费循环；同一实例只允许调用一次，停止信号或启动 Context 取消均会终止底层 Consumer。
func (r *ConsumerRuntime) Start(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return fmt.Errorf("queue consumer runtime %q already started", r.config.Name)
	}
	defer close(r.done)
	if channelClosed(r.stop) {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("queue consumer runtime %q start context is nil", r.config.Name)
	}

	r.logEvent(ctx, logDebug, "queue.consumer.started")
	var state consumerRunState
	runCtx, cancel := newConsumerContext(ctx, &state)
	coordDone := make(chan struct{})
	// 协调 goroutine 只合并两个取消来源；Consumer 返回后的 cancel 保证它也能确定退出。
	go func() {
		defer close(coordDone)
		select {
		case <-r.stop:
			if state.cancellationInitiated() {
				cancel()
			}
		case <-ctx.Done():
			if state.cancellationInitiated() {
				cancel()
			}
		case <-runCtx.Done():
		}
	}()
	defer func() {
		// Consumer 或观测代码 panic 时也必须先标记返回，再取消并等待协调 goroutine。
		state.consumerReturned()
		cancel()
		<-coordDone
	}()

	err := r.consume(runCtx)
	canceledByRuntime := state.consumerReturned()
	if normalized := normalizeConsumerError(err, canceledByRuntime); normalized == nil && err != nil {
		// 仅归一化由 Start Context 或 Stop 信号触发的取消，保留 Consumer 自发返回的同类错误。
		r.logEvent(ctx, logDebug, "queue.consumer.stopped")
		return nil
	}
	if err != nil {
		r.telemetry.RecordRuntimeFailure(ctx, r.config.Destination, r.config.Name)
		r.logEvent(ctx, logError, "queue.consumer.failed")
		return err
	}
	r.logEvent(ctx, logDebug, "queue.consumer.stopped")
	return nil
}

// Stop 发出一次停止信号；Runtime 已启动时等待其完全退出，等待时间受传入 Context 限制。
func (r *ConsumerRuntime) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() { close(r.stop) })
	if !r.started.Load() {
		// 先关闭 stop 再观察 started，使并发到达的 Start 必定在调用 Consumer 前看见停止信号。
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("queue consumer runtime %q stop context is nil", r.config.Name)
	}
	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ConsumerRuntime) consume(ctx context.Context) error {
	return r.consumer.Consume(ctx, r.handleDelivery)
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// consumerRunState 在线性化点记录 Consumer 返回与 Runtime 发起取消的先后关系。
// 互斥区只保护本地布尔状态，不包含 Handler、网络调用或 Context 取消回调。
type consumerRunState struct {
	mu                     sync.Mutex
	returned               bool
	cancellationWasStarted bool
}

func (s *consumerRunState) consumerReturned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.returned = true
	return s.cancellationWasStarted
}

func (s *consumerRunState) cancellationInitiated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.returned {
		return false
	}
	s.cancellationWasStarted = true
	return true
}

func normalizeConsumerError(err error, canceledByRuntime bool) error {
	if cancellationOnlyErrorTree(err) && canceledByRuntime {
		return nil
	}
	return err
}

// cancellationOnlyErrorTree 遍历完整错误树；只有每个非空叶子都表示 Context
// 取消时才允许 Runtime 把 Consumer 返回值归一为正常停止。
func cancellationOnlyErrorTree(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		hasChild := false
		for _, child := range joined.Unwrap() {
			if child == nil {
				continue
			}
			hasChild = true
			if !cancellationOnlyErrorTree(child) {
				return false
			}
		}
		if hasChild {
			return true
		}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil {
			return cancellationOnlyErrorTree(child)
		}
	}
	return errors.Is(err, context.Canceled)
}

type consumerCancellationSource struct {
	context context.Context
	state   *consumerRunState
}

type consumerSourceContextKey struct{}

// newConsumerContext 保留父 Context 的 Value 和 Deadline，但只允许协调 goroutine 实际关闭 Done。
func newConsumerContext(
	parent context.Context,
	state *consumerRunState,
) (context.Context, context.CancelFunc) {
	source := consumerCancellationSource{context: parent, state: state}
	detached := context.WithValue(context.WithoutCancel(parent), consumerSourceContextKey{}, source)
	if deadline, ok := parent.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return context.WithCancel(detached)
}

func consumerContextErr(ctx context.Context) error {
	if source, ok := ctx.Value(consumerSourceContextKey{}).(consumerCancellationSource); ok {
		if err := source.context.Err(); err != nil {
			source.state.cancellationInitiated()
			return err
		}
	}
	return ctx.Err()
}
