package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/errtree"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/lifecycle"
	internaltelemetry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka/internal/telemetry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// RuntimeConfig 描述一个业务消费者及其统一重试和死信策略。
type RuntimeConfig struct {
	// Name 必填的消费者观测名称，用于日志和指标，不替代 Kafka Group 配置。
	Name string
	// Destination 必填的消费目标观测名称，不改变 Consumer 实际订阅的 Topic。
	Destination string
	// Retry 进程内重试策略；nil 默认尝试 3 次、初始退避 500ms、上限 30s，构造后固定。
	Retry *RetryPolicy
	// DeadLetter 不可重试或重试耗尽后的死信生产者；nil 时直接返回失败，由调用方管理其资源。
	DeadLetter Producer
	// DeadLetterDestination 死信观测名称，须与 DeadLetter 一起配置，不改变其实际投递目标。
	DeadLetterDestination string
}

// ConsumerRuntime 将外部 Consumer 绑定到业务 Handler，并实现统一生命周期、重试、死信与观测策略。
type ConsumerRuntime struct {
	// config 构造期校验后的运行配置。
	config RuntimeConfig
	// consumer 负责拉取消息，Start/Stop 控制其消费 Context，不额外关闭外部资源。
	consumer Consumer
	// handler 接收消息的业务处理函数。
	handler Handler
	// log 消费运行时日志。
	log log.Logger
	// telemetry 消息、尝试和死信的追踪及指标。
	telemetry *internaltelemetry.Telemetry
	// retry 已展开默认值并校验的重试策略。
	retry retryPolicy

	// runner 协调单次启动、停止信号和等待 Start 返回。
	runner *lifecycle.Runner
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
		log:       logger.WithModule("kafka"),
		telemetry: telemetry,
		retry:     retry,
		runner:    lifecycle.NewRunner(),
	}
}

// Start 启动消费循环；同一实例只允许调用一次，停止信号或启动 Context 取消均会终止底层 Consumer。
func (r *ConsumerRuntime) Start(ctx context.Context) error {
	err := r.runner.Run(ctx, func(runCtx context.Context) error {
		r.logEvent(ctx, logInfo, "kafka.consumer.started", "kafka consumer started")
		err := r.consumer.Consume(runCtx, r.handleDelivery)
		// 先于检查错误内容快照取消状态：只有 Consume 返回时运行 Context 已被 Stop 或启动
		// Context 取消，纯取消错误才归一为正常停止；Consumer 自发返回的同类错误原样保留。
		canceledByRuntime := runCtx.Err() != nil
		if err == nil || errtree.Only(err, context.Canceled) && canceledByRuntime {
			r.logEvent(ctx, logInfo, "kafka.consumer.stopped", "kafka consumer stopped")
			return nil
		}
		r.telemetry.RecordRuntimeFailure(ctx, r.config.Destination, r.config.Name)
		r.logEvent(ctx, logError, "kafka.consumer.failed", "kafka consumer failed")
		return err
	})
	if errors.Is(err, lifecycle.ErrAlreadyStarted) {
		return fmt.Errorf("queue consumer runtime %q already started", r.config.Name)
	}
	return err
}

// Stop 发出一次停止信号；Runtime 已启动时等待其完全退出，等待时间受传入 Context 限制。
func (r *ConsumerRuntime) Stop(ctx context.Context) error {
	return r.runner.Stop(ctx)
}
