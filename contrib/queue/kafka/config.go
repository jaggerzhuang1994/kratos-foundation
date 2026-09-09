package kafka

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

const (
	// maximumConcurrency 限制一次 Consume 创建的独立 Kafka Client 数。
	maximumConcurrency    = 1024
	defaultMaxPollRecords = 1
	// maximumPollRecords 约束单次在内存中解码的 Kafka Record 数。
	maximumPollRecords = 10_000
)

// ProducerConfig 描述一个 Kafka Producer 使用的连接与 Topic。
type ProducerConfig struct {
	// Connection 引用 pkg/kafka.ClientFactory 中的具名连接。
	Connection string
	// Topic 是 Producer 写入的 Kafka Topic。
	Topic string
}

// ConsumerConfig 描述一个 Kafka Consumer Group 及其并发、起始位点和单次 Poll 上限。
type ConsumerConfig struct {
	// Connection 引用 pkg/kafka.ClientFactory 中的具名连接。
	Connection string
	// Topic 是 Consumer 订阅的 Kafka Topic。
	Topic string
	// Group 是 Kafka Consumer Group 名称。
	Group string
	// Instance 是单并发实例名或多并发实例名前缀。
	Instance string
	// Concurrency 是每次 Consume 创建的独立 Client 数，零值为 1。
	Concurrency int
	// StartPosition 是新 Group 的起始位点，零值为 queue.StartEarliest。
	StartPosition queue.StartPosition
	// MaxPollRecords 是单次 Poll 的最大记录数，零值为 1。
	MaxPollRecords int
}

// normalizeProducerConfig 清理并校验 Producer 必需字段。
func normalizeProducerConfig(config ProducerConfig) (ProducerConfig, error) {
	config.Connection = strings.TrimSpace(config.Connection)
	config.Topic = strings.TrimSpace(config.Topic)
	if config.Connection == "" {
		return ProducerConfig{}, errors.New("kafka queue producer connection is required")
	}
	if config.Topic == "" {
		return ProducerConfig{}, errors.New("kafka queue producer topic is required")
	}
	return config, nil
}

// normalizeConsumerConfig 清理、补齐并校验 Consumer 配置。
func normalizeConsumerConfig(config ConsumerConfig) (ConsumerConfig, error) {
	config.Connection = strings.TrimSpace(config.Connection)
	config.Topic = strings.TrimSpace(config.Topic)
	config.Group = strings.TrimSpace(config.Group)
	config.Instance = strings.TrimSpace(config.Instance)
	if config.Connection == "" {
		return ConsumerConfig{}, errors.New("kafka queue consumer connection is required")
	}
	if config.Topic == "" {
		return ConsumerConfig{}, errors.New("kafka queue consumer topic is required")
	}
	if config.Group == "" {
		return ConsumerConfig{}, errors.New("kafka queue consumer group is required")
	}
	if config.Instance == "" {
		return ConsumerConfig{}, errors.New("kafka queue consumer instance is required")
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.Concurrency < 0 {
		return ConsumerConfig{}, errors.New("kafka queue consumer concurrency must be positive")
	}
	if config.Concurrency > maximumConcurrency {
		return ConsumerConfig{}, fmt.Errorf(
			"kafka queue consumer concurrency must not exceed %d",
			maximumConcurrency,
		)
	}
	if config.StartPosition == 0 {
		config.StartPosition = queue.StartEarliest
	}
	if config.StartPosition != queue.StartEarliest &&
		config.StartPosition != queue.StartLatest {
		return ConsumerConfig{}, fmt.Errorf(
			"kafka queue consumer has unsupported start position %d",
			config.StartPosition,
		)
	}
	if config.MaxPollRecords == 0 {
		config.MaxPollRecords = defaultMaxPollRecords
	}
	if config.MaxPollRecords < 0 {
		return ConsumerConfig{}, errors.New("kafka queue consumer max poll records must be positive")
	}
	if config.MaxPollRecords > maximumPollRecords {
		return ConsumerConfig{}, fmt.Errorf(
			"kafka queue consumer max poll records must not exceed %d",
			maximumPollRecords,
		)
	}
	return config, nil
}
