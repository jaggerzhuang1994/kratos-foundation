package redis

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

const (
	defaultBlockTimeout = 5 * time.Second
	defaultClaimIdle    = time.Minute
	// maximumConcurrency 限制一次 Consume 启动的 Redis Consumer 实例数。
	maximumConcurrency = 1024
	// minimumClaimInterval 限制空队列的 Pending 扫描频率，避免形成 Redis 忙循环。
	minimumClaimInterval = time.Second
	// minimumHeartbeatInterval 限制长 Handler 心跳频率，避免大量消息并发时压垮 Redis。
	minimumHeartbeatInterval = 100 * time.Millisecond
	// minimumBlockTimeout 避免小于 Redis 毫秒精度的时间被编码为 BLOCK 0，意外变成无限阻塞。
	minimumBlockTimeout = time.Millisecond
	// minimumClaimIdle 为 100ms 心跳至少留出三个周期，避免正常 Handler 被过早回收。
	minimumClaimIdle = 3 * minimumHeartbeatInterval
)

// ProducerConfig 描述一个借用 Redis Manager 连接并写入 Stream 的 Producer。
type ProducerConfig struct {
	// Connection 引用 pkg/redis.Manager 中的具名连接。
	Connection string
	// Stream 是 Producer 写入的 Redis Stream。
	Stream string
	// MaxLength 是 Stream 的最大近似或精确长度；零表示不限制。
	MaxLength int64
	// ApproximateMaxLength 使用 Redis 的近似裁剪，并且要求 MaxLength 大于零。
	ApproximateMaxLength bool
}

// ConsumerConfig 描述一个 Redis Consumer Group 及其读取与 Pending 回收参数。
type ConsumerConfig struct {
	// Connection 引用 pkg/redis.Manager 中的具名连接。
	Connection string
	// Stream 是 Consumer 读取的 Redis Stream。
	Stream string
	// Group 是 Redis Consumer Group 名称。
	Group string
	// Instance 是单并发实例名或多并发实例名前缀。
	Instance string
	// Concurrency 是单次 Consume 的并发实例数，零值为 1。
	Concurrency int
	// StartPosition 是新 Group 的起始位置，零值为 queue.StartEarliest。
	StartPosition queue.StartPosition
	// BlockTimeout 是 XREADGROUP 的最长阻塞时间，零值为 5 秒。
	BlockTimeout time.Duration
	// ClaimIdle 是 Pending 消息可被回收的最短闲置时间，零值为 1 分钟。
	ClaimIdle time.Duration
}

// normalizeProducerConfig 清理并校验 Redis Producer 配置。
func normalizeProducerConfig(config ProducerConfig) (ProducerConfig, error) {
	config.Connection = strings.TrimSpace(config.Connection)
	config.Stream = strings.TrimSpace(config.Stream)
	if config.Connection == "" {
		return ProducerConfig{}, errors.New("redis queue producer connection is required")
	}
	if config.Stream == "" {
		return ProducerConfig{}, errors.New("redis queue producer stream is required")
	}
	if config.MaxLength < 0 {
		return ProducerConfig{}, errors.New("redis queue producer max length cannot be negative")
	}
	if config.ApproximateMaxLength && config.MaxLength == 0 {
		return ProducerConfig{}, errors.New(
			"redis queue producer approximate max length requires a positive max length",
		)
	}
	return config, nil
}

// normalizeConsumerConfig 清理、补齐并校验 Redis Consumer 配置。
func normalizeConsumerConfig(config ConsumerConfig) (ConsumerConfig, error) {
	config.Connection = strings.TrimSpace(config.Connection)
	config.Stream = strings.TrimSpace(config.Stream)
	config.Group = strings.TrimSpace(config.Group)
	config.Instance = strings.TrimSpace(config.Instance)
	if config.Connection == "" {
		return ConsumerConfig{}, errors.New("redis queue consumer connection is required")
	}
	if config.Stream == "" {
		return ConsumerConfig{}, errors.New("redis queue consumer stream is required")
	}
	if config.Group == "" {
		return ConsumerConfig{}, errors.New("redis queue consumer group is required")
	}
	if config.Instance == "" {
		return ConsumerConfig{}, errors.New("redis queue consumer instance is required")
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.Concurrency < 0 {
		return ConsumerConfig{}, errors.New("redis queue consumer concurrency must be positive")
	}
	if config.Concurrency > maximumConcurrency {
		return ConsumerConfig{}, fmt.Errorf(
			"redis queue consumer concurrency must not exceed %d",
			maximumConcurrency,
		)
	}
	if config.StartPosition == 0 {
		config.StartPosition = queue.StartEarliest
	}
	if config.StartPosition != queue.StartEarliest &&
		config.StartPosition != queue.StartLatest {
		return ConsumerConfig{}, fmt.Errorf(
			"redis queue consumer has unsupported start position %d",
			config.StartPosition,
		)
	}
	if config.BlockTimeout == 0 {
		config.BlockTimeout = defaultBlockTimeout
	}
	if config.BlockTimeout < minimumBlockTimeout {
		return ConsumerConfig{}, fmt.Errorf(
			"redis queue consumer block timeout must be at least %s",
			minimumBlockTimeout,
		)
	}
	if config.ClaimIdle == 0 {
		config.ClaimIdle = defaultClaimIdle
	}
	if config.ClaimIdle < minimumClaimIdle {
		return ConsumerConfig{}, fmt.Errorf(
			"redis queue consumer claim idle must be at least %s",
			minimumClaimIdle,
		)
	}
	return config, nil
}

func (c ConsumerConfig) instanceName(index int) string {
	if c.Concurrency <= 1 {
		return c.Instance
	}
	return fmt.Sprintf("%s-%d", c.Instance, index+1)
}
