package kafka

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// maximumConcurrency 限制一次 Consume 创建的独立 Kafka Client 数。
	maximumConcurrency    = 1024
	defaultMaxPollRecords = 1
	// maximumPollRecords 约束单次在内存中解码的 Kafka Record 数。
	maximumPollRecords = 10_000
)

// ProducerConfig 配置 Kafka 生产者。
type ProducerConfig struct {
	// Connection 必填，引用 ClientFactory 中的具名连接。
	Connection string
	// Topic 必填，指定生产者写入的 Kafka Topic。
	Topic string
}

// ConsumerConfig 配置 Kafka 消费者。
type ConsumerConfig struct {
	// Connection 必填，引用 ClientFactory 中的具名连接。
	Connection string
	// Topic 必填，指定消费者订阅的 Kafka Topic。
	Topic string
	// Group 必填，指定 Kafka Consumer Group。
	Group string
	// Instance 必填；单并发时作为实例名，多并发时追加从 1 开始的槽位编号。
	Instance string
	// Concurrency 每次 Consume 创建的独立 Client 数；零值为 1，允许 1 至 1024。
	Concurrency int
	// StartPosition 无可用提交位点时的起始位置；零值为 StartEarliest，也支持 StartLatest。
	StartPosition StartPosition
	// MaxPollRecords 每个 Client 单次 Poll 的记录上限；零值为 1，允许 1 至 10000。
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
		config.StartPosition = StartEarliest
	}
	if config.StartPosition != StartEarliest &&
		config.StartPosition != StartLatest {
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
