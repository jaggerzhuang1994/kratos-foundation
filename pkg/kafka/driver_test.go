package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"

	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"

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
	if config.Concurrency != 1 || config.StartPosition != StartEarliest ||
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
		StartPosition:  StartEarliest,
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
			StartPosition:  StartEarliest,
			MaxPollRecords: 1,
		},
		nil,
		factory.create,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := consumer.Consume(ctx, func(context.Context, Delivery) error {
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
			StartPosition:  StartEarliest,
			MaxPollRecords: 1,
		},
		nil,
		factory.create,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- consumer.Consume(ctx, func(context.Context, Delivery) error { return nil })
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
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
}

func newQueueKafkaFileLogger(t testing.TB) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue-kafka.log")
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared, path
}

func newQueueKafkaManager(
	t testing.TB,
	connections map[string]*config_pb.KafkaConnection,
) *ClientFactory {
	t.Helper()
	manager, err := NewClientFactory(
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
	err = consumer.Consume(context.Background(), func(context.Context, Delivery) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "missing-ca.pem") {
		t.Fatalf("Consume(TLS failure) error = %v", err)
	}
}

func TestConsumerClientOptionsEncodeGroupIdentityAndStartPosition(t *testing.T) {
	for _, test := range []struct {
		name     string
		position StartPosition
		want     kgo.Offset
	}{
		{name: "earliest", position: StartEarliest, want: kgo.NewOffset().AtStart()},
		{name: "latest", position: StartLatest, want: kgo.NewOffset().AtEnd()},
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
	if err := nilConsumer.Consume(context.Background(), func(context.Context, Delivery) error { return nil }); err == nil {
		t.Fatal("nil consumer started")
	}
	consumerValue := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		return nil, nil
	})
	if err := consumerValue.Consume(context.Background(), nil); err == nil {
		t.Fatal("consumer accepted nil handler")
	}
	if err := consumerValue.Consume(context.Background(), func(context.Context, Delivery) error { return nil }); err == nil || !strings.Contains(err.Error(), "returned nil") {
		t.Fatalf("nil client error = %v", err)
	}

	factory := newConsumerLifecycleFactory(1, "", nil)
	running := newConsumer(ConsumerConfig{
		Connection:     "main",
		Topic:          "orders",
		Group:          "billing",
		Instance:       "billing-1",
		Concurrency:    1,
		StartPosition:  StartEarliest,
		MaxPollRecords: 1,
	}, nil, factory.create)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- running.Consume(ctx, func(context.Context, Delivery) error { return nil })
	}()
	select {
	case <-factory.allCreated:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
	if err := running.Consume(context.Background(), func(context.Context, Delivery) error { return nil }); err == nil || !strings.Contains(err.Error(), "already running") {
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

// externalKafkaFactory 只在显式设置测试地址时连接隔离的真实 Broker。
func externalKafkaFactory(t testing.TB) (*ClientFactory, foundationlog.Logger, string) {
	t.Helper()
	address := os.Getenv("FOUNDATION_TEST_KAFKA_ADDR")
	if address == "" {
		t.Skip("set FOUNDATION_TEST_KAFKA_ADDR for Docker integration tests")
	}
	logger, cleanup, err := foundationlog.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	allow := true
	level := "error"
	factory, err := NewClientFactory(logger, testconfig.New(t, "kafka", &config_pb.Kafka{
		Log:         &config_pb.ModuleLog{Level: &level},
		Connections: map[string]*config_pb.KafkaConnection{"external": {Brokers: []string{address}, AllowAutoTopicCreation: &allow}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return factory, logger, fmt.Sprintf("foundation_test_%d", time.Now().UnixNano())
}

func TestExternalKafkaFailureReplaysUncommittedBatch(t *testing.T) {
	factory, logger, topic := externalKafkaFactory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	producer, cleanup, err := NewProducer(factory, ProducerConfig{Connection: "external", Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	messages := make([]*Message, 32)
	for i := range messages {
		messages[i] = &Message{Body: []byte(fmt.Sprint(i))}
	}
	if err := producer.PublishBatch(ctx, messages); err != nil {
		t.Fatal(err)
	}
	consumer, err := NewConsumer(factory, logger, ConsumerConfig{Connection: "external", Topic: topic, Group: topic, Instance: topic, MaxPollRecords: 32})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected handler failure")
	var first string
	if err := consumer.Consume(ctx, func(_ context.Context, d Delivery) error { first = string(d.Message.Body); return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var replay string
	if err := consumer.Consume(ctx, func(_ context.Context, d Delivery) error { replay = string(d.Message.Body); return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if first != "0" || replay != first {
		t.Fatalf("first=%q replay=%q", first, replay)
	}
	// 新会话保持可取消，即使当前没有新消息；已失败的批次未被错误确认。
	stopped, stop := context.WithCancel(ctx)
	stop()
	start := time.Now()
	if err := consumer.Consume(stopped, func(context.Context, Delivery) error { t.Error("canceled consumer executed handler"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	t.Logf("uncommitted replay preserved; canceled stop=%s", time.Since(start))
}

func TestExternalKafkaBrokerRestart(t *testing.T) {
	project := os.Getenv("FOUNDATION_TEST_DOCKER_PROJECT")
	if !strings.HasPrefix(project, "foundation-validation-") {
		t.Skip("requires a runner-owned Docker project")
	}
	factory, logger, topic := externalKafkaFactory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	producer, cleanup, err := NewProducer(factory, ProducerConfig{Connection: "external", Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := producer.Publish(ctx, &Message{Body: []byte("before")}); err != nil {
		t.Fatal(err)
	}
	consumer, err := NewConsumer(factory, logger, ConsumerConfig{Connection: "external", Topic: topic, Group: topic, Instance: topic, MaxPollRecords: 1})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan string, 2)
	done := make(chan error, 1)
	go func() {
		seen := map[string]bool{}
		done <- consumer.Consume(ctx, func(_ context.Context, d Delivery) error {
			value := string(d.Message.Body)
			if !seen[value] {
				seen[value] = true
				events <- value
			}
			return nil
		})
	}()
	// 先确认真实消费已建立，再只重启 runner 创建的 Kafka 服务。
	select {
	case value := <-events:
		if value != "before" {
			t.Fatal(value)
		}
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	compose, err := filepath.Abs("../../testdata/external/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	command := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", compose, "restart", "kafka")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart Kafka: %v %s", err, output)
	}
	if err := producer.Publish(ctx, &Message{Body: []byte("after")}); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-events:
		if value != "after" {
			t.Fatal(value)
		}
	case err := <-done:
		t.Fatalf("consumer stopped during recovery: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Logf("existing producer/consumer recovered after broker restart in %s", time.Since(start))
	cancel()
	start = time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("consumer did not stop")
	}
	t.Logf("active consumer stop=%s", time.Since(start))
}

func TestExternalKafkaClientLifecycleSamples(t *testing.T) {
	factory, _, _ := externalKafkaFactory(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var before, after runtime.MemStats
	for i := 0; i < 21; i++ {
		client, err := factory.NewProducerClient("external")
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Ping(ctx); err != nil {
			client.Close()
			t.Fatal(err)
		}
		client.Close()
		if i == 0 {
			runtime.GC()
			runtime.ReadMemStats(&before)
			t.Logf("warm goroutines=%d heap=%d", runtime.NumGoroutine(), before.HeapAlloc)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("after 20 create/ping/close cycles: goroutines=%d heap=%d heap_delta=%d", runtime.NumGoroutine(), after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc))
}

func TestPublicConsumerFactoryBindsSDKContextAndRecoveryOptions(t *testing.T) {
	manager := newQueueKafkaManager(t, map[string]*config_pb.KafkaConnection{"main": {Brokers: []string{"127.0.0.1:1"}}})
	value, err := NewConsumer(manager, nil, ConsumerConfig{Connection: "main", Topic: "orders", Group: "billing", Instance: "billing-1", StartPosition: StartLatest})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := value.(*consumer).createClient(ctx, "main", "billing-1", kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseAllowingRebalance()
	sdk := client.(*kgo.Client)
	if sdk.OptValue(kgo.WithContext) != ctx {
		t.Fatal("production factory did not bind SDK to worker context")
	}
	if sdk.OptValue(kgo.ConsumeStartOffset).(kgo.Offset).String() != kgo.NewOffset().AtStart().String() {
		t.Fatal("production factory discarded recovery offset option")
	}
	cancel()
}

func testRecord(topic string, partition int32, offset int64) *kgo.Record {
	return &kgo.Record{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
		Key:       []byte("key"),
		Value:     []byte("value"),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: []byte("header")},
		},
		Timestamp: time.Unix(offset, 0),
	}
}

func testFetches(records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "orders",
			Partitions: []kgo.FetchPartition{{
				Partition: 0,
				Records:   records,
			}},
		}},
	}}
}

type recordingCommitter struct {
	mu      sync.Mutex
	records []*kgo.Record
	err     error
	count   int
}

type consumerLifecycleFactory struct {
	mu            sync.Mutex
	want          int
	fatalInstance string
	fatalErr      error
	allCreated    chan struct{}
	createdOnce   sync.Once
	instances     []string
	clients       []*consumerClientStub
}

func newConsumerLifecycleFactory(
	want int,
	fatalInstance string,
	fatalErr error,
) *consumerLifecycleFactory {
	return &consumerLifecycleFactory{
		want:          want,
		fatalInstance: fatalInstance,
		fatalErr:      fatalErr,
		allCreated:    make(chan struct{}),
		clients:       make([]*consumerClientStub, 0, want),
	}
}

func (f *consumerLifecycleFactory) create(
	_ context.Context,
	connection string,
	instance string,
	_ ...kgo.Opt,
) (consumerClient, error) {
	if connection != "main" {
		return nil, errors.New("unexpected connection")
	}
	client := &consumerClientStub{}
	client.poll = func(ctx context.Context, _ int) kgo.Fetches {
		select {
		case <-f.allCreated:
		case <-ctx.Done():
			return kgo.NewErrFetch(ctx.Err())
		}
		if instance == f.fatalInstance {
			return kgo.NewErrFetch(f.fatalErr)
		}
		<-ctx.Done()
		return kgo.NewErrFetch(ctx.Err())
	}
	f.mu.Lock()
	f.instances = append(f.instances, instance)
	f.clients = append(f.clients, client)
	if len(f.clients) == f.want {
		f.createdOnce.Do(func() { close(f.allCreated) })
	}
	f.mu.Unlock()
	return client, nil
}

func (f *consumerLifecycleFactory) snapshot() ([]string, []*consumerClientStub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	instances := append([]string(nil), f.instances...)
	clients := append([]*consumerClientStub(nil), f.clients...)
	return instances, clients
}

type consumerClientStub struct {
	mu             sync.Mutex
	poll           func(context.Context, int) kgo.Fetches
	closed         int
	allowRebalance int
}

func (c *consumerClientStub) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	return c.poll(ctx, maxRecords)
}

func (*consumerClientStub) CommitRecords(context.Context, ...*kgo.Record) error { return nil }

func (*consumerClientStub) LeaveGroupContext(context.Context) error { return nil }

func (c *consumerClientStub) AllowRebalance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowRebalance++
}

func (c *consumerClientStub) CloseAllowingRebalance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
}

func (c *consumerClientStub) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *consumerClientStub) allowRebalanceCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allowRebalance
}

func newRecordingCommitter(err error) *recordingCommitter {
	return &recordingCommitter{err: err}
}

func (c *recordingCommitter) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.records = append([]*kgo.Record(nil), records...)
	return c.err
}

func (c *recordingCommitter) commitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *recordingCommitter) committedRecords() []*kgo.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*kgo.Record(nil), c.records...)
}

type producerClientStub struct {
	mu     sync.Mutex
	closed int
}

func newProducerClientStub() *producerClientStub {
	return &producerClientStub{}
}

func (*producerClientStub) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	results := make(kgo.ProduceResults, len(records))
	for index, record := range records {
		results[index] = kgo.ProduceResult{Record: record}
	}
	return results
}

func (c *producerClientStub) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
}

func (c *producerClientStub) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// TestExternalIntegrationKafkaRoundTrip 验证真实 Broker 上的消息往返和消费进度。
func TestExternalIntegrationKafkaRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		messages             []*Message
		managed, rejectBatch bool
	}{
		{name: "raw explicit identity", messages: []*Message{{ID: "order-1", Key: []byte("tenant"), Body: []byte("订单已创建"), Headers: []Header{{Key: "content-type", Value: []byte("text/plain")}}}}},
		{name: "raw generated delivery identity", messages: []*Message{{Body: []byte("event")}}},
		{name: "binary payload", messages: []*Message{{ID: "binary", Body: []byte{0, 255, 1, 0}, Headers: []Header{{Key: "binary", Value: []byte{0, 254}}}}}},
		{name: "empty payload", messages: []*Message{{ID: "empty"}}},
		{name: "managed batch", managed: true, messages: []*Message{{ID: "one", Body: []byte("1")}, {ID: "two", Body: []byte("2")}, {ID: "three", Body: []byte("3")}}},
		{name: "managed metadata", managed: true, messages: []*Message{{Body: []byte("generated")}}},
		{name: "invalid batch publishes nothing", rejectBatch: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			factory, logger, topic := externalKafkaFactory(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			raw, cleanup, err := NewProducer(factory, ProducerConfig{Connection: "external", Topic: topic})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			producer := raw
			if tt.managed {
				producer, err = NewManagedProducer("orders", raw, newDisabledTestObservability(t))
				if err != nil {
					t.Fatal(err)
				}
			}
			if tt.rejectBatch {
				if err := producer.PublishBatch(ctx, []*Message{{ID: "must-not-publish"}, nil}); err == nil {
					t.Fatal("nil batch entry accepted")
				}
			} else {
				before := make([]*Message, len(tt.messages))
				for i, message := range tt.messages {
					before[i] = message.Clone()
				}
				if len(tt.messages) == 1 {
					err = producer.Publish(ctx, tt.messages[0])
				} else {
					err = producer.PublishBatch(ctx, tt.messages)
				}
				if err != nil {
					t.Fatal(err)
				}
				for i, message := range tt.messages {
					if message.ID != before[i].ID || !message.Timestamp.Equal(before[i].Timestamp) || !reflect.DeepEqual(message.Headers, before[i].Headers) && len(message.Headers)+len(before[i].Headers) != 0 {
						t.Fatal("publish changed caller metadata")
					}
				}
			}
			if err := producer.PublishBatch(ctx, nil); err != nil {
				t.Fatalf("empty batch: %v", err)
			}
			// 单分区且每轮只处理一条；遇到末尾标记时，前面每条消息已经同步提交。
			if err := raw.Publish(ctx, &Message{ID: "barrier"}); err != nil {
				t.Fatal(err)
			}
			consumer, err := NewConsumer(factory, logger, ConsumerConfig{Connection: "external", Topic: topic, Group: topic, Instance: topic, MaxPollRecords: 1})
			if err != nil {
				t.Fatal(err)
			}
			finished := errors.New("test reached barrier")
			index := 0
			err = consumer.Consume(ctx, func(_ context.Context, delivery Delivery) error {
				if delivery.Err != nil {
					return delivery.Err
				}
				message := delivery.Message
				if message == nil {
					return errors.New("missing delivery message")
				}
				if message.ID == "barrier" {
					return finished
				}
				if index >= len(tt.messages) {
					return fmt.Errorf("unexpected extra message %q", message.ID)
				}
				want := tt.messages[index]
				if !bytes.Equal(message.Body, want.Body) || !bytes.Equal(message.Key, want.Key) || !reflect.DeepEqual(message.Headers, want.Headers) && len(message.Headers)+len(want.Headers) != 0 {
					return fmt.Errorf("message %d payload mismatch: %#v", index, message)
				}
				if message.ID == "" || want.ID != "" && message.ID != want.ID {
					return fmt.Errorf("message %d identity mismatch: %q", index, message.ID)
				}
				if message.Timestamp.IsZero() {
					return errors.New("missing timestamp")
				}
				index++
				return nil
			})
			if !errors.Is(err, finished) || index != len(tt.messages) {
				t.Fatalf("first session: delivered=%d want=%d err=%v", index, len(tt.messages), err)
			}
			// 同组重开只能重放未确认的标记，不应重放前面已确认的业务消息。
			err = consumer.Consume(ctx, func(_ context.Context, delivery Delivery) error {
				if delivery.Err != nil {
					return delivery.Err
				}
				if delivery.Message == nil || delivery.Message.ID != "barrier" {
					return fmt.Errorf("committed message replayed: %#v", delivery)
				}
				return finished
			})
			if !errors.Is(err, finished) {
				t.Fatalf("resume committed group: %v", err)
			}
			// 使用者全部退出后才释放；不再调用已关闭 SDK 的发布接口。
			cleanup()
			cleanup()
		})
	}
}
