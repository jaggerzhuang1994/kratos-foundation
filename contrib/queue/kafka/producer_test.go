package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kgo"
)

type scriptedProducerClient struct {
	mu      sync.Mutex
	records []*kgo.Record
	result  func([]*kgo.Record) kgo.ProduceResults
	closed  int
}

func (c *scriptedProducerClient) ProduceSync(
	_ context.Context,
	records ...*kgo.Record,
) kgo.ProduceResults {
	c.mu.Lock()
	c.records = append([]*kgo.Record(nil), records...)
	result := c.result
	c.mu.Unlock()
	if result != nil {
		return result(records)
	}
	results := make(kgo.ProduceResults, len(records))
	for index, record := range records {
		results[index] = kgo.ProduceResult{Record: record}
	}
	return results
}

func (c *scriptedProducerClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
}

func (c *scriptedProducerClient) snapshot() ([]*kgo.Record, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*kgo.Record(nil), c.records...), c.closed
}

func TestProducerPublishEncodesIndependentRecordAndWrapsFailure(t *testing.T) {
	client := &scriptedProducerClient{}
	value, cleanup, err := newProducer(client, ProducerConfig{Connection: " main ", Topic: " orders "})
	if err != nil {
		t.Fatal(err)
	}
	producer := value.(*producer)
	message := &queue.Message{
		ID:        "message-1",
		Key:       []byte("key"),
		Body:      []byte("body"),
		Headers:   []queue.Header{{Key: messageIDHeader, Value: []byte("stale")}, {Key: "trace", Value: []byte("trace-1")}},
		Timestamp: time.Unix(100, 0),
	}
	if err := producer.Publish(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	records, _ := client.snapshot()
	if len(records) != 1 {
		t.Fatalf("produced records = %d, want 1", len(records))
	}
	record := records[0]
	if record.Topic != "orders" || string(record.Key) != "key" || string(record.Value) != "body" || !record.Timestamp.Equal(message.Timestamp) {
		t.Fatalf("encoded record = %#v", record)
	}
	if len(record.Headers) != 2 || record.Headers[0].Key != "trace" || string(record.Headers[0].Value) != "trace-1" || record.Headers[1].Key != messageIDHeader || string(record.Headers[1].Value) != "message-1" {
		t.Fatalf("encoded headers = %#v", record.Headers)
	}
	message.Key[0] = 'X'
	message.Body[0] = 'X'
	message.Headers[1].Value[0] = 'X'
	if string(record.Key) != "key" || string(record.Value) != "body" || string(record.Headers[0].Value) != "trace-1" {
		t.Fatalf("record aliases source message: %#v", record)
	}
	cleanup()
	cleanup()
	if _, closes := client.snapshot(); closes != 1 {
		t.Fatalf("client closes = %d, want 1", closes)
	}

	wantErr := errors.New("broker rejected record")
	failing := &scriptedProducerClient{result: func(records []*kgo.Record) kgo.ProduceResults {
		return kgo.ProduceResults{{Record: records[0], Err: wantErr}}
	}}
	failingValue, _, err := newProducer(failing, ProducerConfig{Connection: "main", Topic: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	err = failingValue.Publish(context.Background(), &queue.Message{ID: "message-2"})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "orders") {
		t.Fatalf("Publish failure = %v", err)
	}
}

func TestProducerPublishRejectsInvalidStateAndInput(t *testing.T) {
	var producer *producer
	if err := producer.Publish(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "message is nil") {
		t.Fatalf("nil message error = %v", err)
	}
	if err := producer.Publish(context.Background(), &queue.Message{}); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("nil producer error = %v", err)
	}
	if value, cleanup, err := newProducer(nil, ProducerConfig{Connection: "main", Topic: "orders"}); value != nil || cleanup != nil || err == nil {
		t.Fatalf("newProducer(nil client) = (producer nil=%t, cleanup nil=%t, %v)", value == nil, cleanup == nil, err)
	}
	client := &scriptedProducerClient{}
	if value, cleanup, err := newProducer(client, ProducerConfig{}); value != nil || cleanup != nil || err == nil {
		t.Fatalf("newProducer(invalid config) = (producer nil=%t, cleanup nil=%t, %v)", value == nil, cleanup == nil, err)
	}
}

func TestProducerPublishBatchAlignsOutOfOrderFailuresToInputs(t *testing.T) {
	wantFirst := errors.New("first failed")
	wantThird := errors.New("third failed")
	client := &scriptedProducerClient{result: func(records []*kgo.Record) kgo.ProduceResults {
		return kgo.ProduceResults{
			{Record: records[2], Err: wantThird},
			{Record: records[1]},
			{Record: records[0], Err: wantFirst},
		}
	}}
	value, _, err := newProducer(client, ProducerConfig{Connection: "main", Topic: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	messages := []*queue.Message{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	err = value.PublishBatch(context.Background(), messages)
	var batchErr *queue.BatchError
	if !errors.As(err, &batchErr) || !errors.Is(err, wantFirst) || !errors.Is(err, wantThird) {
		t.Fatalf("PublishBatch error = %v", err)
	}
	if len(batchErr.Failures) != 2 ||
		batchErr.Failures[0].Index != 0 || batchErr.Failures[0].MessageID != "one" ||
		batchErr.Failures[1].Index != 2 || batchErr.Failures[1].MessageID != "three" {
		t.Fatalf("batch failures = %#v", batchErr.Failures)
	}
}

func TestProducerPublishBatchHandlesEmptyNilAndSuccessfulInputs(t *testing.T) {
	var nilProducer *producer
	if err := nilProducer.PublishBatch(context.Background(), nil); err != nil {
		t.Fatalf("empty batch error = %v", err)
	}
	if err := nilProducer.PublishBatch(context.Background(), []*queue.Message{{ID: "one"}}); err == nil {
		t.Fatal("nil producer accepted non-empty batch")
	}
	client := &scriptedProducerClient{}
	value, _, err := newProducer(client, ProducerConfig{Connection: "main", Topic: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if err := value.PublishBatch(context.Background(), []*queue.Message{{ID: "one"}, nil}); err == nil || !strings.Contains(err.Error(), "message 1 is nil") {
		t.Fatalf("nil batch message error = %v", err)
	}
	if err := value.PublishBatch(context.Background(), []*queue.Message{{ID: "one"}, {ID: "two"}}); err != nil {
		t.Fatal(err)
	}
	if records, _ := client.snapshot(); len(records) != 2 {
		t.Fatalf("successful batch records = %d, want 2", len(records))
	}
}

func TestCollectBatchFailuresRejectsMalformedProducerResults(t *testing.T) {
	messages := []*queue.Message{{ID: "one"}, {ID: "two"}}
	records := []*kgo.Record{{Topic: "orders"}, {Topic: "orders"}}
	unknown := &kgo.Record{Topic: "other"}
	for _, test := range []struct {
		name    string
		results kgo.ProduceResults
		want    string
	}{
		{
			name: "duplicate",
			results: kgo.ProduceResults{
				{Record: records[0]}, {Record: records[0]},
			},
			want: "duplicate",
		},
		{
			name:    "omitted",
			results: kgo.ProduceResults{{Record: records[0]}},
			want:    "omitted batch result 1",
		},
		{
			name: "unknown",
			results: kgo.ProduceResults{
				{Record: records[0]}, {Record: records[1]}, {Record: unknown},
			},
			want: "unknown",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures, err := collectBatchFailures(messages, records, test.results)
			if failures != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("collectBatchFailures() = (%#v, %v)", failures, err)
			}
		})
	}
}
