package queue

import "context"

// Delivery 是一次业务投递的独立消息及领取信息，不暴露租约 Token。
// Attempt 包含本次领取和执行前崩溃的领取次数，MaxAttempts 来自消费者配置。
type Delivery[T any] struct {
	ID          string
	Message     T
	Attempt     int
	MaxAttempts int
}

// CanRetry 表示领取预算尚未耗尽，不保证重试成功；永久错误仍然不会重试。
func (d Delivery[T]) CanRetry() bool { return d.Attempt > 0 && d.Attempt < d.MaxAttempts }

// DeliveryHandler 处理一次类型化投递，必须响应 Context 取消并保证业务幂等。
type DeliveryHandler[T any] func(context.Context, Delivery[T]) error

// Handle 将只需要业务消息的处理方法适配为投递处理器。
func Handle[T any](handler func(context.Context, T) error) DeliveryHandler[T] {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, delivery Delivery[T]) error { return handler(ctx, delivery.Message) }
}

// HandleDelivery 接入需要任务 ID 或真实领取次数的处理方法。
func HandleDelivery[T any](handler func(context.Context, Delivery[T]) error) DeliveryHandler[T] {
	return handler
}

// Middleware 装饰消费处理器；按传入顺序从外向内执行，实例须支持并发调用。
// 装饰器在构造期调用一次；运行时必须把 Context 和 Delivery 传给下游。
type Middleware[T any] func(DeliveryHandler[T]) DeliveryHandler[T]

type deliveryMetadata struct{ attempt, maxAttempts int }
type deliveryMetadataKey struct{}
