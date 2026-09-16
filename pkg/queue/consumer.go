package queue

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// ConsumerConfig 在构造时固化，零值启用消费并使用默认参数，不支持热更新。
type ConsumerConfig struct {
	Name           string                                    // 空值使用 Definition.Queue。
	Disabled       bool                                      // 禁用时 Start 等待停止，不领取任务；仍校验配置和依赖。
	Concurrency    int                                       // 默认 1，最大 1024。
	Timeout        time.Duration                             // 默认 30s，包含领取、限流等待和处理耗时。
	MaxAttempts    int                                       // 默认 3；Retry 非 nil 时必须为零，避免两处定义冲突。
	PollInterval   time.Duration                             // 默认 200ms。
	StorageTimeout time.Duration                             // 默认 5s。
	Lease          time.Duration                             // 默认 max(60s, Timeout + StorageTimeout + 1s)。
	Retry          *RetryPolicy                              // 可选高级退避配置，沿用 Worker 的显式零值语义。
	Before         func(context.Context) error               // 解码校验后、处理中间件前等待共享限流器；失败消耗本次领取。
	Classify       func(error) error                         // 将执行错误映射为 Permanent 或普通可重试错误；返回 nil 保留原错误。
	OnFailed       func(context.Context, FailureEvent) error // 仅归档成功后通知，非可靠消息。
}

// Consumer 实现 Start/Stop Runtime 契约，复用 Worker 的领取、重试、租约和启停机制。
// 不导入 app/bootstrap；组装层直接 RegisterRuntime(consumer)，并在停止后释放 Store 连接。
type Consumer[T any] struct{ worker *Worker }

// NewConsumer 创建单消息类型消费者，构造期不访问 Store；middleware 按参数顺序从外向内执行。
func NewConsumer[T any](definition Definition[T], store Store, handler DeliveryHandler[T], config ConsumerConfig, observability Observability, middleware ...Middleware[T]) (*Consumer[T], error) {
	definition, err := definition.resolve()
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("queue consumer handler is nil")
	}
	for i := len(middleware) - 1; i >= 0; i-- {
		if middleware[i] == nil {
			return nil, errors.New("queue consumer middleware is nil")
		}
		handler = middleware[i](handler)
		if handler == nil {
			return nil, errors.New("queue consumer middleware returned nil")
		}
	}
	workerConfig, err := config.workerConfig(definition.Queue)
	if err != nil {
		return nil, err
	}
	adapted := func(ctx context.Context, task *Task) error {
		message, err := definition.Codec.Decode(task.Payload)
		if err != nil {
			return Permanent(fmt.Errorf("%w: %w", errMessageDecode, err))
		}
		if definition.Validate != nil {
			if err := definition.Validate(message); err != nil {
				return Permanent(fmt.Errorf("%w: %w", errMessageValidation, err))
			}
		}
		// 仅 Worker 在本次执行上下文写入元数据，生产者 Header 不能覆盖领取事实。
		metadata := ctx.Value(deliveryMetadataKey{}).(deliveryMetadata)
		if config.Before != nil {
			err = config.Before(ctx)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = handler(ctx, Delivery[T]{ID: task.ID, Message: message, Attempt: metadata.attempt, MaxAttempts: metadata.maxAttempts})
		}
		// 坏消息和已标记的永久错误不交给分类器降级为重试。
		if err != nil && !IsPermanent(err) && config.Classify != nil {
			if classified := config.Classify(err); classified != nil {
				err = classified
			}
		}
		return err
	}
	worker, err := NewWorker(workerConfig, store, map[string]Handler{definition.taskType(): adapted}, observability)
	if err != nil {
		return nil, err
	}
	worker.disabled, worker.onFailed = config.Disabled, config.OnFailed
	return &Consumer[T]{worker: worker}, nil
}

// Start 阻塞运行消费者；同一实例只能启动一次，包括禁用的实例。
func (c *Consumer[T]) Start(ctx context.Context) error { return c.worker.Start(ctx) }

// Stop 幂等取消消费并等待处理退出，不关闭 Store 连接。
func (c *Consumer[T]) Stop(ctx context.Context) error { return c.worker.Stop(ctx) }

func (c ConsumerConfig) workerConfig(queue string) (WorkerConfig, error) {
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
			return WorkerConfig{}, errors.New("invalid queue consumer execution window")
		}
		c.Lease = max(60*time.Second, c.Timeout+c.StorageTimeout+time.Second)
	}
	if c.Retry != nil && c.MaxAttempts != 0 {
		return WorkerConfig{}, errors.New("queue consumer max attempts conflicts with retry policy")
	}
	if c.Retry == nil {
		if c.MaxAttempts == 0 {
			c.MaxAttempts = defaultMaxAttempts
		}
		c.Retry = &RetryPolicy{MaxAttempts: c.MaxAttempts, MinBackoff: defaultMinBackoff, MaxBackoff: defaultMaxBackoff}
	}
	return WorkerConfig{Name: c.Name, Queue: queue, Concurrency: c.Concurrency, PollInterval: c.PollInterval, Timeout: c.Timeout, StorageTimeout: c.StorageTimeout, Lease: c.Lease, Retry: c.Retry}, nil
}
