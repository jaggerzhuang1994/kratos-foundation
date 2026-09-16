package queue

// Endpoint 将同一队列的发布与消费一次构造完成，供应用入口使用。
// 直接提供 Publish/PublishWith 和 Start/Stop；仅发布的进程应使用 NewPublisher。
// 多队列可在组装层匿名嵌入独立命名结构体以区分 Wire 实例，业务只依赖自身小接口。
type Endpoint[T any] struct {
	*Publisher[T]
	*Consumer[T]
}

// NewEndpoint 使用相同定义及 Store 构造发布和消费入口，不启动运行时或访问 Store。
func NewEndpoint[T any](definition Definition[T], store Store, handler DeliveryHandler[T], config ConsumerConfig, observability Observability, middleware ...Middleware[T]) (*Endpoint[T], error) {
	publisher, err := NewPublisher(definition, store, observability)
	if err != nil {
		return nil, err
	}
	consumer, err := NewConsumer(definition, store, handler, config, observability, middleware...)
	if err != nil {
		return nil, err
	}
	return &Endpoint[T]{Publisher: publisher, Consumer: consumer}, nil
}
