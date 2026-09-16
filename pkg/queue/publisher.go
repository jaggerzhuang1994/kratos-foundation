package queue

import (
	"context"
	"fmt"
	"time"
)

// PublishOptions 设置稳定任务 ID、最早投递时间和业务 Header；零值表示即时投递并生成 ID。
// Header 不参与领取次数判定，调用期间不得并发修改输入。
type PublishOptions struct {
	ID          string
	AvailableAt time.Time
	Headers     map[string]string
}

// Publisher 是可独立构造的类型化投递入口，不启动消费、不拥有 Store 连接。
type Publisher[T any] struct {
	definition Definition[T]
	dispatcher *Dispatcher
}

// NewPublisher 校验并固化消息定义，不访问 Store。
func NewPublisher[T any](definition Definition[T], store Store, observability Observability) (*Publisher[T], error) {
	definition, err := definition.resolve()
	if err != nil {
		return nil, err
	}
	dispatcher, err := NewDispatcher(definition.Queue, store, observability)
	if err != nil {
		return nil, err
	}
	return &Publisher[T]{definition: definition, dispatcher: dispatcher}, nil
}

// Publish 校验并即时投递消息，返回任务 ID；事务及不确定提交边界与 Dispatcher 相同。
func (p *Publisher[T]) Publish(ctx context.Context, message T) (string, error) {
	return p.PublishWith(ctx, message, PublishOptions{})
}

// PublishWith 在编码前校验消息，失败时不访问 Store；不修改 options 中的 Header。
func (p *Publisher[T]) PublishWith(ctx context.Context, message T, options PublishOptions) (string, error) {
	if p.definition.Validate != nil {
		if err := p.definition.Validate(message); err != nil {
			return "", fmt.Errorf("validate queue message: %w", err)
		}
	}
	payload, err := p.definition.Codec.Encode(message)
	if err != nil {
		return "", fmt.Errorf("encode queue message: %w", err)
	}
	return p.dispatcher.Dispatch(ctx, &Task{ID: options.ID, Type: p.definition.taskType(), Payload: payload, Headers: options.Headers, AvailableAt: options.AvailableAt})
}
