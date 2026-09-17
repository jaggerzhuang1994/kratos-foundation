package kafka

import (
	"context"
)

// Handler 在 Consumer 提供的 Context 中处理一条消息。
type Handler func(context.Context, *Message) error

// Delivery 将驱动投递结果交给统一处理流程，包括损坏记录。
type Delivery struct {
	// Message 解码后的消息；解码失败时可能为 nil。
	Message *Message
	// Err 本次投递的解码错误，可进入失败或死信流程。
	Err error
}

// DeliveryHandler 处理一次驱动投递，包括解码失败。
type DeliveryHandler func(context.Context, Delivery) error

// Producer 通过具体后端适配器持有的资源发布消息。
type Producer interface {
	// Publish 发布一条消息。
	Publish(context.Context, *Message) error
	// PublishBatch 按输入顺序发布多条消息。
	PublishBatch(context.Context, []*Message) error
}

// Consumer 在 Consume 中阻塞，直到 Context 取消或后端失败；取消 Context 就是调用方的关停机制。
type Consumer interface {
	// Consume 交付已解码消息或解码错误。
	Consume(context.Context, DeliveryHandler) error
}

// StartPosition 指定新建 Consumer Group 的起始位置。
type StartPosition uint8

const (
	// StartEarliest 从仍被保留的最旧消息开始。
	StartEarliest StartPosition = iota + 1
	// StartLatest 从已有消息之后开始。
	StartLatest
)
