// Package outbox 实现 Kafka 事务外盒：业务在数据库事务内写入待发消息，事务提交后由 Relay
// 通过 Producer 发布，保证“业务写成功则消息最终发出、业务回滚则消息不发”。
//
// 待发消息存放在支持事务投递的 queue.Store（通常是 contrib/queue/database 的数据库 Store）中，
// Relay 是一个 queue.Worker，复用队列的租约、重试、失败归档、SKIP LOCKED 领取与续租能力。
package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// Message 是待发布的 Kafka 消息。
type Message struct {
	// ID 为外盒记录与 Kafka 消息的共同标识；空值由 Write 生成 UUID。
	// 业务用稳定 ID（如“order-created:<订单号>”）时，消费者可据此去重；记录仍在外盒中（待发、失败，或 Repo 开启
	// RetainCompleted 保留已发记录）时重复 Write 返回 queue.ErrDuplicate，默认发布成功后删除记录，同一 ID 可再次写入。
	ID string `json:"id"`
	// Topic 选择 Relay 中对应的 Producer，必填。
	Topic string `json:"topic"`
	// Key 为分区键。
	Key []byte `json:"key,omitempty"`
	// Body 为消息正文。
	Body []byte `json:"body,omitempty"`
	// Headers 为业务消息头；追踪上下文由 Relay 发布时注入，不需要业务填写。
	Headers []kafka.Header `json:"headers,omitempty"`
	// Timestamp 为消息时间，零值由 Write 设为写入时刻（不是发布时刻）。
	Timestamp time.Time `json:"timestamp"`
}

// Outbox 写入待发消息；只借用 Store，不拥有连接。
type Outbox struct {
	queue *queue.Queue[Message]
}

// New 创建外盒。name 是逻辑队列名，用于观测与 Relay 的 Worker 名；Store 必须支持与业务事务一起投递
// （数据库 Store 的 Repo.Insert 复用 Context 中的事务），否则无法保证与业务数据同时提交。
func New(name string, store queue.Store, observability queue.Observability) (*Outbox, error) {
	q, err := queue.NewQueue(queue.Definition[Message]{Queue: name, Validate: validate}, store, observability)
	if err != nil {
		return nil, fmt.Errorf("create kafka outbox: %w", err)
	}
	return &Outbox{queue: q}, nil
}

func validate(message Message) error {
	if strings.TrimSpace(message.Topic) == "" {
		return errors.New("outbox message topic is required")
	}
	for _, header := range message.Headers {
		if strings.TrimSpace(header.Key) == "" {
			return errors.New("outbox message header key is required")
		}
	}
	return nil
}

// Write 在 ctx 携带的业务事务中写入一条待发消息，返回消息 ID。事务提交后 Relay 才能领取；
// 事务回滚则消息一起撤销。成功只表示已写入事务，不代表已提交或已发布。
func (o *Outbox) Write(ctx context.Context, message Message) (string, error) {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now().UTC()
	}
	return o.queue.PostWith(ctx, message, queue.PostOptions{ID: message.ID})
}

// RelayConfig 控制 Relay 的发布行为。
type RelayConfig struct {
	// Producers 按 Topic 提供已构造的 Producer（建议经 kafka.NewManagedProducer 包装以获得指标与追踪）；
	// 消息 Topic 不在其中时直接失败归档，不重试。
	Producers map[string]kafka.Producer
	// Worker 为底层队列 Worker 配置；Name 空值使用外盒名，Concurrency 默认 1。
	// MaxAttempts 与 Retry 都未设置时默认 100 次、1s 起倍增、上限 1min（约 1.5 小时的 Broker 故障窗口）。
	Worker queue.WorkerConfig
}

// Relay 创建发布已提交消息的 Runtime，由组装层 spec.RegisterRuntime 登记。
// 发布失败按重试策略稍后重发，耗尽次数后进入失败归档，可经队列运维接口查看与重试。
// 语义是至少一次：发布成功后、确认前崩溃会再次发布同一 ID 的消息，消费者须按 ID 幂等。
// Concurrency 为 1 时大致按写入顺序发布，但失败重试的消息会排到后面；需要按 Key 严格有序时不要依赖 Relay 顺序。
func (o *Outbox) Relay(config RelayConfig) (*queue.Worker[Message], error) {
	if len(config.Producers) == 0 {
		return nil, errors.New("outbox relay requires at least one producer")
	}
	producers := make(map[string]kafka.Producer, len(config.Producers))
	for topic, producer := range config.Producers {
		if strings.TrimSpace(topic) == "" || producer == nil {
			return nil, errors.New("outbox relay producers require non-empty topics and non-nil producers")
		}
		producers[topic] = producer
	}
	worker := config.Worker
	if worker.MaxAttempts == 0 && worker.Retry == nil {
		worker.Retry = &queue.RetryPolicy{MaxAttempts: 100, MinBackoff: time.Second, MaxBackoff: time.Minute}
	}
	return o.queue.Worker(func(ctx context.Context, message Message) error {
		producer, ok := producers[message.Topic]
		if !ok {
			return queue.Permanent(fmt.Errorf("outbox topic %q has no producer", message.Topic))
		}
		return producer.Publish(ctx, &kafka.Message{
			ID: message.ID, Key: message.Key, Body: message.Body,
			Headers: append([]kafka.Header(nil), message.Headers...), Timestamp: message.Timestamp,
		})
	}, worker)
}
