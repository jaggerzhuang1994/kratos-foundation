package redis

import (
	"bytes"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestNormalizeConsumerConfigAppliesRedisDefaults(t *testing.T) {
	config, err := normalizeConsumerConfig(ConsumerConfig{
		Connection: "main",
		Stream:     "orders",
		Group:      "billing",
		Instance:   "billing-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Concurrency != 1 || config.StartPosition != queue.StartEarliest ||
		config.BlockTimeout != 5*time.Second || config.ClaimIdle != time.Minute {
		t.Fatalf("defaults = %#v", config)
	}
}

func TestNormalizeProducerConfigRejectsApproximateWithoutMaxLength(t *testing.T) {
	_, err := normalizeProducerConfig(ProducerConfig{
		Connection:           "main",
		Stream:               "orders",
		ApproximateMaxLength: true,
	})
	if err == nil {
		t.Fatal("accepted approximate max length without max length")
	}
}

func TestNormalizeConfigsRejectRedisLimits(t *testing.T) {
	producer := ProducerConfig{Connection: "main", Stream: "orders"}
	for _, invalid := range []ProducerConfig{
		{Stream: "orders"},
		{Connection: "main"},
		{Connection: "main", Stream: "orders", MaxLength: -1},
	} {
		if _, err := normalizeProducerConfig(invalid); err == nil {
			t.Fatalf("accepted producer config %#v", invalid)
		}
	}
	if _, err := normalizeProducerConfig(producer); err != nil {
		t.Fatal(err)
	}

	consumer := ConsumerConfig{
		Connection:    "main",
		Stream:        "orders",
		Group:         "billing",
		Instance:      "billing-1",
		Concurrency:   1,
		StartPosition: queue.StartEarliest,
		BlockTimeout:  time.Millisecond,
		ClaimIdle:     300 * time.Millisecond,
	}
	tests := []struct {
		name   string
		mutate func(*ConsumerConfig)
	}{
		{name: "connection", mutate: func(config *ConsumerConfig) { config.Connection = " " }},
		{name: "stream", mutate: func(config *ConsumerConfig) { config.Stream = " " }},
		{name: "group", mutate: func(config *ConsumerConfig) { config.Group = " " }},
		{name: "instance", mutate: func(config *ConsumerConfig) { config.Instance = " " }},
		{name: "negative concurrency", mutate: func(config *ConsumerConfig) { config.Concurrency = -1 }},
		{name: "maximum concurrency", mutate: func(config *ConsumerConfig) { config.Concurrency = 1025 }},
		{name: "start position", mutate: func(config *ConsumerConfig) { config.StartPosition = 99 }},
		{name: "minimum block", mutate: func(config *ConsumerConfig) { config.BlockTimeout = time.Millisecond - 1 }},
		{name: "minimum claim", mutate: func(config *ConsumerConfig) { config.ClaimIdle = 300*time.Millisecond - 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := consumer
			test.mutate(&invalid)
			if _, err := normalizeConsumerConfig(invalid); err == nil {
				t.Fatalf("accepted consumer config %#v", invalid)
			}
		})
	}
	if normalized, err := normalizeConsumerConfig(consumer); err != nil {
		t.Fatal(err)
	} else if heartbeatInterval(normalized.ClaimIdle) != 100*time.Millisecond {
		t.Fatalf("heartbeat interval = %s", heartbeatInterval(normalized.ClaimIdle))
	}
	consumer.Concurrency = 1024
	if _, err := normalizeConsumerConfig(consumer); err != nil {
		t.Fatalf("rejected inclusive maximums: %v", err)
	}
}

func TestRedisMessageEncodingRoundTripsThroughDeliveryDecoder(t *testing.T) {
	timestamp := time.Date(2026, time.August, 31, 9, 30, 0, 123, time.UTC)
	want := &queue.Message{
		ID:        "message-1",
		Key:       []byte("order-1"),
		Body:      []byte(`{"status":"paid"}`),
		Headers:   []queue.Header{{Key: "traceparent", Value: []byte("trace-1")}},
		Timestamp: timestamp,
	}
	args := newXAddArgs(ProducerConfig{
		Stream:               "orders",
		MaxLength:            100,
		ApproximateMaxLength: true,
	}, want)
	if args.Stream != "orders" || args.MaxLen != 100 || !args.Approx {
		t.Fatalf("XADD args = %#v", args)
	}
	values, ok := args.Values.(map[string]any)
	if !ok {
		t.Fatalf("XADD values type = %T", args.Values)
	}
	got, err := decodeMessage(goredis.XMessage{ID: "redis-1", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || !bytes.Equal(got.Key, want.Key) || !bytes.Equal(got.Body, want.Body) ||
		!got.Timestamp.Equal(want.Timestamp) || len(got.Headers) != 1 ||
		got.Headers[0].Key != want.Headers[0].Key || !bytes.Equal(got.Headers[0].Value, want.Headers[0].Value) {
		t.Fatalf("decoded message = %#v, want %#v", got, want)
	}
}

func TestDirectConstructorsResolveAndBorrowManagerClient(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	manager := &recordingManager{client: client}

	produced, err := NewProducer(manager, ProducerConfig{
		Connection: " main ",
		Stream:     " orders ",
	})
	if err != nil {
		t.Fatal(err)
	}
	producer, ok := produced.(*producer)
	if !ok || producer.client != client || producer.config.Stream != "orders" {
		t.Fatalf("producer = %#v", produced)
	}

	consumed, err := NewConsumer(manager, nil, ConsumerConfig{
		Connection: " main ",
		Stream:     " orders ",
		Group:      " billing ",
		Instance:   " billing-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := consumed.(*consumer)
	if !ok {
		t.Fatalf("consumer type = %T, want *consumer", consumed)
	}
	operations, operationsOK := consumer.operations.(*redisConsumerOperations)
	if !operationsOK || operations.client != client || consumer.config.Stream != "orders" {
		t.Fatalf("consumer = %#v", consumed)
	}
	if len(manager.connections) != 2 || manager.connections[0] != "main" || manager.connections[1] != "main" {
		t.Fatalf("resolved connections = %#v", manager.connections)
	}
}
