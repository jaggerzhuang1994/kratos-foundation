// Package redis 提供直接构造的 Redis Streams 队列 Producer 与 Consumer 适配器。
package redis

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
)

// NewProducer 解析并借用 Redis Manager 的具名 Client，创建直接写入 Stream 的 Producer。
// Producer 不拥有 Client，因此不会关闭 Manager 的共享连接。
func NewProducer(
	manager foundationredis.Manager,
	config ProducerConfig,
) (queue.Producer, error) {
	normalized, err := normalizeProducerConfig(config)
	if err != nil {
		return nil, err
	}
	client, err := manager.Connection(normalized.Connection)
	if err != nil {
		return nil, fmt.Errorf("resolve redis connection %q: %w", normalized.Connection, err)
	}
	if client == nil {
		return nil, fmt.Errorf(
			"resolve redis connection %q: manager returned a nil client",
			normalized.Connection,
		)
	}
	return &producer{client: client, config: normalized}, nil
}

// NewConsumer 解析并借用 Redis Manager 的具名 Client，创建由 Context 管理生命周期的 Consumer。
// Consumer 不拥有 Client，因此不会关闭 Manager 的共享连接。
func NewConsumer(
	manager foundationredis.Manager,
	logger log.Logger,
	config ConsumerConfig,
) (queue.Consumer, error) {
	normalizedConfig, err := normalizeConsumerConfig(config)
	if err != nil {
		return nil, err
	}
	client, err := manager.Connection(normalizedConfig.Connection)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve redis connection %q: %w",
			normalizedConfig.Connection,
			err,
		)
	}
	if client == nil {
		return nil, fmt.Errorf(
			"resolve redis connection %q: manager returned a nil client",
			normalizedConfig.Connection,
		)
	}
	return newConsumer(
		normalizedConfig,
		logger,
		&redisConsumerOperations{client: client},
	), nil
}
