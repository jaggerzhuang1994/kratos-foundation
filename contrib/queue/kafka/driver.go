// Package kafka 提供直接构造的 Kafka 队列 Producer 与 Consumer 适配器。
package kafka

import (
	"context"
	"fmt"

	foundationkafka "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kgo"
)

// NewProducer 创建独占一个 all-ISR franz-go Client 的 Kafka Producer。
// 返回的 cleanup 幂等关闭该 Client，调用方负责在 Producer 停止使用后调用它。
func NewProducer(
	manager *foundationkafka.ClientFactory,
	config ProducerConfig,
) (queue.Producer, func(), error) {
	normalized, err := normalizeProducerConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if !manager.HasConnection(normalized.Connection) {
		return nil, nil, fmt.Errorf(
			"kafka connection %q is not configured",
			normalized.Connection,
		)
	}
	// 每个 Producer 固定要求所有 ISR 确认，连接级较弱策略不能覆盖队列可靠性边界。
	client, err := manager.NewProducerClient(
		normalized.Connection,
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create Kafka producer client: %w", err)
	}
	if client == nil {
		return nil, nil, fmt.Errorf(
			"create Kafka producer client %q: manager returned nil",
			normalized.Connection,
		)
	}
	producer, cleanup, err := newProducer(client, normalized)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return producer, cleanup, nil
}

// NewConsumer 创建借用 ClientFactory 的 Kafka Consumer；每次 Consume 才按并发数创建独立 Client。
func NewConsumer(
	manager *foundationkafka.ClientFactory,
	logger log.Logger,
	config ConsumerConfig,
) (queue.Consumer, error) {
	normalized, err := normalizeConsumerConfig(config)
	if err != nil {
		return nil, err
	}
	if !manager.HasConnection(normalized.Connection) {
		return nil, fmt.Errorf(
			"kafka connection %q is not configured",
			normalized.Connection,
		)
	}
	return newConsumer(
		normalized,
		logger,
		func(ctx context.Context, connection string, instance string, options ...kgo.Opt) (consumerClient, error) {
			clientOptions := consumerClientOptions(normalized, instance)
			clientOptions = append(clientOptions, options...)
			clientOptions = append(clientOptions, kgo.WithContext(ctx))
			client, err := manager.NewConsumerClient(connection, clientOptions...)
			if err != nil || client == nil {
				return nil, err
			}
			return client, nil
		},
	), nil
}

// consumerClientOptions 固定 Consumer Group、实例名、起始位点和手动提交边界。
func consumerClientOptions(config ConsumerConfig, instance string) []kgo.Opt {
	start := kgo.NewOffset().AtStart()
	if config.StartPosition == queue.StartLatest {
		start = kgo.NewOffset().AtEnd()
	}
	return []kgo.Opt{
		kgo.ConsumeTopics(config.Topic),
		kgo.ConsumerGroup(config.Group),
		kgo.ClientID(instance),
		kgo.ConsumeStartOffset(start),
		kgo.ConsumeResetOffset(start),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	}
}
