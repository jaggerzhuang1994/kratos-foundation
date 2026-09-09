package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kgo"
)

// producerClient 是 Producer 独占的 franz-go 网络与关闭边界。
type producerClient interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
	Close()
}

// newProducer 将已创建的 Client 交给单个 Producer，并返回仅关闭一次的 cleanup。
func newProducer(
	client producerClient,
	config ProducerConfig,
) (queue.Producer, func(), error) {
	if client == nil {
		return nil, nil, errors.New("kafka queue producer client is nil")
	}
	normalized, err := normalizeProducerConfig(config)
	if err != nil {
		return nil, nil, err
	}
	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(client.Close)
	}
	return &producer{client: client, config: normalized}, cleanup, nil
}

type producer struct {
	client producerClient
	config ProducerConfig
}

var _ queue.Producer = (*producer)(nil)

// Publish 同步将一条消息写入 Producer 配置的 Kafka Topic。
func (p *producer) Publish(ctx context.Context, message *queue.Message) error {
	if message == nil {
		return errors.New("kafka queue message is nil")
	}
	if p == nil || p.client == nil {
		return errors.New("kafka queue producer is not initialized")
	}
	if err := p.client.ProduceSync(ctx, encodeRecord(p.config.Topic, message)).FirstErr(); err != nil {
		return fmt.Errorf("produce Kafka record to %q: %w", p.config.Topic, err)
	}
	return nil
}

// PublishBatch 同步写入多条消息，并以 queue.BatchError 报告逐条失败。
func (p *producer) PublishBatch(ctx context.Context, messages []*queue.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if p == nil || p.client == nil {
		return errors.New("kafka queue producer is not initialized")
	}
	for index, message := range messages {
		if message == nil {
			return fmt.Errorf("kafka queue message %d is nil", index)
		}
	}
	records := make([]*kgo.Record, len(messages))
	for index, message := range messages {
		records[index] = encodeRecord(p.config.Topic, message)
	}
	results := p.client.ProduceSync(ctx, records...)
	failures, err := collectBatchFailures(messages, records, results)
	if err != nil {
		return err
	}
	if len(failures) > 0 {
		return &queue.BatchError{Failures: failures}
	}
	return nil
}

// collectBatchFailures 按 Record 指针将 franz-go 的完成顺序重新对齐到输入顺序。
func collectBatchFailures(
	messages []*queue.Message,
	records []*kgo.Record,
	results kgo.ProduceResults,
) ([]queue.BatchFailure, error) {
	resultErrors := make(map[*kgo.Record]error, len(results))
	for _, result := range results {
		if _, exists := resultErrors[result.Record]; exists {
			return nil, errors.New("kafka producer returned a duplicate batch result")
		}
		resultErrors[result.Record] = result.Err
	}
	failures := make([]queue.BatchFailure, 0)
	for index, record := range records {
		resultErr, exists := resultErrors[record]
		if !exists {
			return nil, fmt.Errorf("kafka producer omitted batch result %d", index)
		}
		if resultErr != nil {
			failures = append(failures, queue.BatchFailure{
				Index:     index,
				MessageID: messages[index].ID,
				Err:       resultErr,
			})
		}
	}
	if len(resultErrors) != len(records) {
		return nil, errors.New("kafka producer returned an unknown batch result")
	}
	return failures, nil
}
