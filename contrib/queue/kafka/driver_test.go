package kafka

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationkafka "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestNormalizeProducerConfig(t *testing.T) {
	config, err := normalizeProducerConfig(ProducerConfig{
		Connection: " main ",
		Topic:      " orders ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Connection != "main" || config.Topic != "orders" {
		t.Fatalf("config = %#v", config)
	}
	for _, invalid := range []ProducerConfig{
		{Topic: "orders"},
		{Connection: "main"},
		{Connection: " \t", Topic: "orders"},
		{Connection: "main", Topic: " \n"},
	} {
		if _, err := normalizeProducerConfig(invalid); err == nil {
			t.Fatalf("accepted invalid config %#v", invalid)
		}
	}
}

func TestNormalizeConsumerConfigAppliesDefaults(t *testing.T) {
	config, err := normalizeConsumerConfig(ConsumerConfig{
		Connection: " main ",
		Topic:      " orders ",
		Group:      " billing ",
		Instance:   " billing-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Connection != "main" || config.Topic != "orders" ||
		config.Group != "billing" || config.Instance != "billing-1" {
		t.Fatalf("config = %#v", config)
	}
	if config.Concurrency != 1 || config.StartPosition != queue.StartEarliest ||
		config.MaxPollRecords != 1 {
		t.Fatalf("defaults = %#v", config)
	}
}

func TestNormalizeConsumerConfigRejectsInvalidValues(t *testing.T) {
	valid := ConsumerConfig{
		Connection:     "main",
		Topic:          "orders",
		Group:          "billing",
		Instance:       "billing-1",
		Concurrency:    1,
		StartPosition:  queue.StartEarliest,
		MaxPollRecords: 1,
	}
	tests := []struct {
		name   string
		mutate func(*ConsumerConfig)
	}{
		{name: "connection", mutate: func(config *ConsumerConfig) { config.Connection = " " }},
		{name: "topic", mutate: func(config *ConsumerConfig) { config.Topic = " " }},
		{name: "group", mutate: func(config *ConsumerConfig) { config.Group = " " }},
		{name: "instance", mutate: func(config *ConsumerConfig) { config.Instance = " " }},
		{name: "negative concurrency", mutate: func(config *ConsumerConfig) { config.Concurrency = -1 }},
		{name: "maximum concurrency", mutate: func(config *ConsumerConfig) { config.Concurrency = 1025 }},
		{name: "start position", mutate: func(config *ConsumerConfig) { config.StartPosition = 99 }},
		{name: "negative poll records", mutate: func(config *ConsumerConfig) { config.MaxPollRecords = -1 }},
		{name: "maximum poll records", mutate: func(config *ConsumerConfig) { config.MaxPollRecords = 10_001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := normalizeConsumerConfig(config); err == nil {
				t.Fatalf("accepted invalid config %#v", config)
			}
		})
	}
}

func TestConsumerConsumeCreatesAndClosesEveryConcurrencyClientOnError(t *testing.T) {
	wantErr := errors.New("fatal fetch")
	factory := newConsumerLifecycleFactory(2, "billing-1", wantErr)
	consumer := newConsumer(
		ConsumerConfig{
			Connection:     "main",
			Topic:          "orders",
			Group:          "billing",
			Instance:       "billing",
			Concurrency:    2,
			StartPosition:  queue.StartEarliest,
			MaxPollRecords: 1,
		},
		nil,
		factory.create,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := consumer.Consume(ctx, func(context.Context, queue.Delivery) error {
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Consume error = %v, want fatal fetch", err)
	}
	instances, clients := factory.snapshot()
	slices.Sort(instances)
	if len(instances) != 2 || instances[0] != "billing-1" || instances[1] != "billing-2" {
		t.Fatalf("created instances = %v, want billing-1 and billing-2", instances)
	}
	for index, client := range clients {
		if client.closeCount() != 1 || client.allowRebalanceCount() != 2 {
			t.Fatalf(
				"client %d closes=%d allow_rebalance=%d, want 1/1",
				index,
				client.closeCount(),
				client.allowRebalanceCount(),
			)
		}
	}
}

func TestConsumerConsumePropagatesCancellationToEveryConcurrencyClient(t *testing.T) {
	factory := newConsumerLifecycleFactory(2, "", nil)
	consumer := newConsumer(
		ConsumerConfig{
			Connection:     "main",
			Topic:          "orders",
			Group:          "billing",
			Instance:       "billing",
			Concurrency:    2,
			StartPosition:  queue.StartEarliest,
			MaxPollRecords: 1,
		},
		nil,
		factory.create,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- consumer.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	}()
	select {
	case <-factory.allCreated:
	case <-time.After(time.Second):
		t.Fatal("Kafka clients were not created")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume error = %v, want context canceled", err)
	}
	_, clients := factory.snapshot()
	if len(clients) != 2 {
		t.Fatalf("clients = %d, want 2", len(clients))
	}
	for index, client := range clients {
		if client.closeCount() != 1 {
			t.Fatalf("client %d close count = %d, want 1", index, client.closeCount())
		}
	}
}

func TestProducerCleanupClosesOwnedClientOnce(t *testing.T) {
	client := newProducerClientStub()
	producer, cleanup, err := newProducer(client, ProducerConfig{Connection: "main", Topic: "orders"})
	if err != nil || producer == nil {
		t.Fatalf("newProducer() = %v, %v", producer, err)
	}
	cleanup()
	cleanup()
	if got := client.closeCount(); got != 1 {
		t.Fatalf("client close count = %d, want 1", got)
	}
}

func newQueueKafkaTestLogger(t testing.TB) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: foundationlog.FileConfig{OutputConfig: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared)
}

func newQueueKafkaFileLogger(t testing.TB) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue-kafka.log")
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     foundationlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared), path
}

func newQueueKafkaManager(
	t testing.TB,
	connections map[string]*config_pb.KafkaConnection,
) *foundationkafka.ClientFactory {
	t.Helper()
	manager, err := foundationkafka.NewClientFactory(
		newQueueKafkaTestLogger(t),
		testconfig.New(t, "kafka", &config_pb.Kafka{Connections: connections}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestPublicConstructorsValidateAndBuildWithoutDialing(t *testing.T) {
	producerConfig := ProducerConfig{Connection: "main", Topic: "orders"}
	consumerConfig := ConsumerConfig{
		Connection: "main",
		Topic:      "orders",
		Group:      "billing",
		Instance:   "billing-1",
	}
	if producer, cleanup, err := NewProducer(nil, ProducerConfig{}); producer != nil || cleanup != nil || err == nil || !strings.Contains(err.Error(), "connection is required") {
		t.Fatalf("NewProducer(invalid config) = (producer nil=%t, cleanup nil=%t, %v)", producer == nil, cleanup == nil, err)
	}
	if consumer, err := NewConsumer(nil, nil, ConsumerConfig{}); consumer != nil || err == nil || !strings.Contains(err.Error(), "connection is required") {
		t.Fatalf("NewConsumer(invalid config) = (%v, %v)", consumer, err)
	}

	emptyManager := newQueueKafkaManager(t, nil)
	if producer, cleanup, err := NewProducer(emptyManager, producerConfig); producer != nil || cleanup != nil || err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("NewProducer(unknown connection) = (producer nil=%t, cleanup nil=%t, %v)", producer == nil, cleanup == nil, err)
	}
	if consumer, err := NewConsumer(emptyManager, nil, consumerConfig); consumer != nil || err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("NewConsumer(unknown connection) = (%v, %v)", consumer, err)
	}

	manager := newQueueKafkaManager(t, map[string]*config_pb.KafkaConnection{
		"main": {Brokers: []string{"127.0.0.1:1"}},
	})
	producer, cleanup, err := NewProducer(manager, ProducerConfig{Connection: " main ", Topic: " orders "})
	if err != nil || producer == nil || cleanup == nil {
		t.Fatalf("NewProducer(valid) = (producer nil=%t, cleanup nil=%t, %v)", producer == nil, cleanup == nil, err)
	}
	cleanup()
	cleanup()
	consumer, err := NewConsumer(manager, newQueueKafkaTestLogger(t), ConsumerConfig{
		Connection: " main ",
		Topic:      " orders ",
		Group:      " billing ",
		Instance:   " billing-1 ",
	})
	if err != nil || consumer == nil {
		t.Fatalf("NewConsumer(valid) = (%v, %v)", consumer, err)
	}
}

func TestPublicConstructorsPropagateLocalClientConfigurationFailure(t *testing.T) {
	missingCA := filepath.Join(t.TempDir(), "missing-ca.pem")
	manager := newQueueKafkaManager(t, map[string]*config_pb.KafkaConnection{
		"secure": {
			Brokers: []string{"127.0.0.1:1"},
			Tls:     &config_pb.KafkaTLS{CaFile: &missingCA},
		},
	})
	producer, cleanup, err := NewProducer(manager, ProducerConfig{Connection: "secure", Topic: "orders"})
	if producer != nil || cleanup != nil || err == nil || !strings.Contains(err.Error(), "missing-ca.pem") {
		t.Fatalf("NewProducer(TLS failure) = (producer nil=%t, cleanup nil=%t, %v)", producer == nil, cleanup == nil, err)
	}
	consumer, err := NewConsumer(manager, nil, ConsumerConfig{
		Connection: "secure",
		Topic:      "orders",
		Group:      "billing",
		Instance:   "billing-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = consumer.Consume(context.Background(), func(context.Context, queue.Delivery) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "missing-ca.pem") {
		t.Fatalf("Consume(TLS failure) error = %v", err)
	}
}

func TestConsumerClientOptionsEncodeGroupIdentityAndStartPosition(t *testing.T) {
	for _, test := range []struct {
		name     string
		position queue.StartPosition
		want     kgo.Offset
	}{
		{name: "earliest", position: queue.StartEarliest, want: kgo.NewOffset().AtStart()},
		{name: "latest", position: queue.StartLatest, want: kgo.NewOffset().AtEnd()},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ConsumerConfig{
				Topic:         "orders",
				Group:         "billing",
				StartPosition: test.position,
			}
			client, err := kgo.NewClient(append(
				[]kgo.Opt{kgo.SeedBrokers("127.0.0.1:1")},
				consumerClientOptions(config, "billing-2")...,
			)...)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if got := client.GetConsumeTopics(); !reflect.DeepEqual(got, []string{"orders"}) {
				t.Errorf("topics = %#v", got)
			}
			if got := client.OptValue(kgo.ConsumerGroup); got != "billing" {
				t.Errorf("consumer group = %#v", got)
			}
			if got := client.OptValue(kgo.ClientID); got != "billing-2" {
				t.Errorf("client ID = %#v", got)
			}
			for _, option := range []any{kgo.ConsumeStartOffset, kgo.ConsumeResetOffset} {
				got, ok := client.OptValue(option).(kgo.Offset)
				if !ok || got.String() != test.want.String() {
					t.Errorf("start offset = %#v, want %s", got, test.want.String())
				}
			}
			if got := client.OptValue(kgo.DisableAutoCommit); got != true {
				t.Errorf("DisableAutoCommit = %#v", got)
			}
			if got := client.OptValue(kgo.BlockRebalanceOnPoll); got != true {
				t.Errorf("BlockRebalanceOnPoll = %#v", got)
			}
		})
	}
}

func TestConsumerRejectsInvalidRuntimeStateAndConcurrentRun(t *testing.T) {
	var nilConsumer *consumer
	if err := nilConsumer.Consume(context.Background(), func(context.Context, queue.Delivery) error { return nil }); err == nil {
		t.Fatal("nil consumer started")
	}
	consumerValue := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		return nil, nil
	})
	if err := consumerValue.Consume(context.Background(), nil); err == nil {
		t.Fatal("consumer accepted nil handler")
	}
	if err := consumerValue.Consume(context.Background(), func(context.Context, queue.Delivery) error { return nil }); err == nil || !strings.Contains(err.Error(), "returned nil") {
		t.Fatalf("nil client error = %v", err)
	}

	factory := newConsumerLifecycleFactory(1, "", nil)
	running := newConsumer(ConsumerConfig{
		Connection:     "main",
		Topic:          "orders",
		Group:          "billing",
		Instance:       "billing-1",
		Concurrency:    1,
		StartPosition:  queue.StartEarliest,
		MaxPollRecords: 1,
	}, nil, factory.create)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- running.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	}()
	select {
	case <-factory.allCreated:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
	if err := running.Consume(context.Background(), func(context.Context, queue.Delivery) error { return nil }); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent Consume error = %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Consume error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop")
	}
}

func TestConsumerInstanceUsesExactSingleNameAndNumberedConcurrentNames(t *testing.T) {
	if got := consumerInstance(ConsumerConfig{Instance: "billing", Concurrency: 1}, 0); got != "billing" {
		t.Fatalf("single instance = %q", got)
	}
	if got := consumerInstance(ConsumerConfig{Instance: "billing", Concurrency: 2}, 1); got != "billing-2" {
		t.Fatalf("concurrent instance = %q", got)
	}
}
