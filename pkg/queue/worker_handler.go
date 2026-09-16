package queue

import (
	"context"
	"errors"
	"fmt"
)

// Execution 是一次已领取任务的处理信息，不表示向队列发布任务，也不暴露租约 Token。
// Message 是独立解码的业务消息；Attempt 包含本次领取及执行前崩溃的领取次数，
// 不是实际调用处理方法的次数。MaxAttempts 来自 Worker 的处理配置。
type Execution[T any] struct {
	ID          string
	Message     T
	Attempt     int
	MaxAttempts int
}

// CanRetry 表示领取预算尚未耗尽，不保证重试成功；永久错误仍然不会重试。
func (e Execution[T]) CanRetry() bool { return e.Attempt > 0 && e.Attempt < e.MaxAttempts }

// ExecutionHandler 处理已领取并解码的任务，必须响应 Context 取消并保证业务幂等。
// 仅适配层需要任务 ID 或领取次数时使用，普通业务直接向 Queue.Worker 传入消息处理方法。
type ExecutionHandler[T any] func(context.Context, Execution[T]) error

// Middleware 装饰任务处理方法，按传入顺序从外向内执行，不参与消息发布。
// 装饰器在构造期调用一次；运行时实例须并发安全，并向下游传递 Context 和 Execution。
type Middleware[T any] func(ExecutionHandler[T]) ExecutionHandler[T]

type executionMetadata struct{ attempt, maxAttempts int }
type executionMetadataKey struct{}

// Worker 为队列创建独立消费运行时，直接接收业务处理方法。
// 构造不启动运行时或访问 Store；仅发布的进程无需调用。
func (q *Queue[T]) Worker(handle func(context.Context, T) error, config WorkerConfig, middleware ...Middleware[T]) (*Worker[T], error) {
	if handle == nil {
		return nil, errors.New("queue message handler is nil")
	}
	return q.WorkerWithExecution(func(ctx context.Context, execution Execution[T]) error {
		return handle(ctx, execution.Message)
	}, config, middleware...)
}

// WorkerWithExecution 创建需要任务 ID 或真实领取次数的消费运行时。
// 普通业务使用 Worker；middleware 按传入顺序从外向内包裹处理方法。
func (q *Queue[T]) WorkerWithExecution(handler ExecutionHandler[T], config WorkerConfig, middleware ...Middleware[T]) (*Worker[T], error) {
	definition := q.definition
	if handler == nil {
		return nil, errors.New("queue processing handler is nil")
	}
	for i := len(middleware) - 1; i >= 0; i-- {
		if middleware[i] == nil {
			return nil, errors.New("queue processing middleware is nil")
		}
		handler = middleware[i](handler)
		if handler == nil {
			return nil, errors.New("queue processing middleware returned nil")
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
		metadata := ctx.Value(executionMetadataKey{}).(executionMetadata)
		if config.BeforeHandle != nil {
			err = config.BeforeHandle(ctx)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = handler(ctx, Execution[T]{ID: task.ID, Message: message, Attempt: metadata.attempt, MaxAttempts: metadata.maxAttempts})
		}
		// 坏消息和已标记的永久错误不交给分类器降级为重试。
		if err != nil && !IsPermanent(err) && config.ClassifyError != nil {
			if classified := config.ClassifyError(err); classified != nil {
				err = classified
			}
		}
		return err
	}
	worker, err := newWorker[T](workerConfig, q.store, map[string]taskHandler{definition.taskType(): adapted}, q.observability)
	if err != nil {
		return nil, err
	}
	worker.disabled, worker.onFailed = config.DisableProcessing, config.OnFailed
	return worker, nil
}
