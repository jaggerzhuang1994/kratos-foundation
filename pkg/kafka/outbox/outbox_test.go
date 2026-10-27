package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	queuegorm "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database/gorm"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka/outbox"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type txKey struct{}

// txProvider 模拟 pkg/database.Manager：Context 中有事务时返回事务，否则返回独立会话。
type txProvider struct{ db *gorm.DB }

func (p txProvider) Connection(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return p.db.WithContext(ctx)
}

func (p txProvider) transaction(ctx context.Context, fn func(context.Context) error) error {
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// recordingProducer 记录发布的消息；failures 次之前返回 err。
type recordingProducer struct {
	mu        sync.Mutex
	messages  []*kafka.Message
	failures  int
	attempts  int
	published chan struct{}
}

func (p *recordingProducer) Publish(_ context.Context, message *kafka.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.attempts <= p.failures {
		return errors.New("broker unavailable")
	}
	p.messages = append(p.messages, message)
	p.published <- struct{}{}
	return nil
}

func (p *recordingProducer) PublishBatch(ctx context.Context, messages []*kafka.Message) error {
	for _, message := range messages {
		if err := p.Publish(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

func observability(t *testing.T) queue.Observability {
	t.Helper()
	logger, cleanupLog, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	info := appinfo.New("outbox-test")
	mp, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	tp, cleanupTracing, err := tracing.NewProvider(testconfig.Empty(t), info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)
	return queue.Observability{Logger: logger, Metrics: mp, Tracing: tp}
}

func newOutbox(t *testing.T) (*outbox.Outbox, txProvider, queue.Store) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbox.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE orders (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	provider := txProvider{db: db}
	repo, err := queuegorm.NewSimpleRepo(context.Background(), provider, queuegorm.SimpleConfig{Table: "kafka_outbox"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := databasequeue.NewStore(repo)
	box, err := outbox.New("orders-outbox", store, observability(t))
	if err != nil {
		t.Fatal(err)
	}
	return box, provider, store
}

func runRelay(t *testing.T, box *outbox.Outbox, config outbox.RelayConfig) func() {
	t.Helper()
	if config.Worker.PollInterval == 0 {
		config.Worker.PollInterval = 10 * time.Millisecond
	}
	relay, err := box.Relay(config)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- relay.Start(context.Background()) }()
	return func() {
		if err := relay.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func waitPublished(t *testing.T, producer *recordingProducer) {
	t.Helper()
	select {
	case <-producer.published:
	case <-time.After(10 * time.Second):
		t.Fatal("message was not published")
	}
}

// 业务事务提交后 Relay 发布同一 ID 的消息；回滚的事务不发布。
func TestOutboxPublishesOnlyCommittedMessages(t *testing.T) {
	box, provider, _ := newOutbox(t)
	ctx := context.Background()
	rejected := errors.New("business rejected")
	at := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	write := func(order string, fail error) error {
		return provider.transaction(ctx, func(ctx context.Context) error {
			if err := provider.Connection(ctx).Exec("INSERT INTO orders (id) VALUES (?)", order).Error; err != nil {
				return err
			}
			if _, err := box.Write(ctx, outbox.Message{ID: "order-created:" + order, Topic: "orders", Key: []byte(order), Body: []byte(`{"id":"` + order + `"}`), Headers: []kafka.Header{{Key: "type", Value: []byte("order.created")}}, Timestamp: at}); err != nil {
				return err
			}
			return fail
		})
	}
	if err := write("o-1", rejected); !errors.Is(err, rejected) {
		t.Fatalf("rollback err = %v", err)
	}
	if err := write("o-2", nil); err != nil {
		t.Fatal(err)
	}
	// 待发期间同一稳定 ID 再次写入被拒绝，避免重复事件；整个业务事务随之回滚。
	err := provider.transaction(ctx, func(ctx context.Context) error {
		_, err := box.Write(ctx, outbox.Message{ID: "order-created:o-2", Topic: "orders"})
		return err
	})
	if !errors.Is(err, queue.ErrDuplicate) {
		t.Fatalf("duplicate write = %v", err)
	}
	producer := &recordingProducer{published: make(chan struct{}, 10)}
	stop := runRelay(t, box, outbox.RelayConfig{Producers: map[string]kafka.Producer{"orders": producer}})
	waitPublished(t, producer)
	time.Sleep(100 * time.Millisecond) // 确认回滚的消息不会随后发布。
	stop()
	if len(producer.messages) != 1 {
		t.Fatalf("published %d messages, want 1", len(producer.messages))
	}
	message := producer.messages[0]
	if message.ID != "order-created:o-2" || string(message.Key) != "o-2" || string(message.Body) != `{"id":"o-2"}` || !message.Timestamp.Equal(at) || len(message.Headers) != 1 || message.Headers[0].Key != "type" {
		t.Fatalf("published message = %+v", message)
	}
}

// Broker 故障时按重试策略重发，恢复后发布成功；没有 Producer 的 Topic 直接失败归档。
func TestOutboxRetriesPublishAndArchivesUnknownTopics(t *testing.T) {
	box, provider, store := newOutbox(t)
	ctx := context.Background()
	for _, topic := range []string{"orders", "unknown"} {
		if err := provider.transaction(ctx, func(ctx context.Context) error {
			_, err := box.Write(ctx, outbox.Message{Topic: topic, Body: []byte(topic)})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	producer := &recordingProducer{failures: 2, published: make(chan struct{}, 10)}
	stop := runRelay(t, box, outbox.RelayConfig{
		Producers: map[string]kafka.Producer{"orders": producer},
		Worker:    queue.WorkerConfig{Retry: &queue.RetryPolicy{MaxAttempts: 5, MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}},
	})
	waitPublished(t, producer)
	var failed []queue.FailedTask
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if failed, err = store.Failed(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if len(failed) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if producer.attempts != 3 || len(producer.messages) != 1 || string(producer.messages[0].Body) != "orders" {
		t.Fatalf("attempts=%d messages=%d", producer.attempts, len(producer.messages))
	}
	if len(failed) != 1 || failed[0].Reason != "permanent" {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestOutboxValidation(t *testing.T) {
	box, provider, _ := newOutbox(t)
	err := provider.transaction(context.Background(), func(ctx context.Context) error {
		_, err := box.Write(ctx, outbox.Message{Body: []byte("x")})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("missing topic = %v", err)
	}
	for _, producers := range []map[string]kafka.Producer{nil, {"": &recordingProducer{}}, {"orders": nil}} {
		if _, err := box.Relay(outbox.RelayConfig{Producers: producers}); err == nil {
			t.Fatalf("relay accepted producers %v", producers)
		}
	}
	if _, err := outbox.New("", nil, queue.Observability{}); err == nil {
		t.Fatal("empty outbox name accepted")
	}
}

// TestExternalOutboxKafka 贯通真实 MySQL 事务写入、Relay 领取与真实 Kafka 发布：
// 消费到的消息带外盒 ID，回滚的事务不发布。
func TestExternalOutboxKafka(t *testing.T) {
	dsn, address := os.Getenv("FOUNDATION_TEST_QUEUE_MYSQL_DSN"), os.Getenv("FOUNDATION_TEST_KAFKA_ADDR")
	if dsn == "" || address == "" {
		t.Skip("set FOUNDATION_TEST_QUEUE_MYSQL_DSN and FOUNDATION_TEST_KAFKA_ADDR for Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	table, orders := "kafka_outbox_"+suffix, "outbox_orders_"+suffix
	t.Cleanup(func() {
		_ = db.Exec("DROP TABLE IF EXISTS " + table).Error
		_ = db.Exec("DROP TABLE IF EXISTS " + orders).Error
	})
	if err := db.Exec("CREATE TABLE " + orders + " (id VARCHAR(64) PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	provider := txProvider{db: db}
	repo, err := queuegorm.NewSimpleRepo(ctx, provider, queuegorm.SimpleConfig{Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	obs := observability(t)
	box, err := outbox.New("orders-outbox", databasequeue.NewStore(repo), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		order string
		fail  bool
	}{{"rolled-back", true}, {"committed", false}} {
		err := provider.transaction(ctx, func(ctx context.Context) error {
			if err := provider.Connection(ctx).Exec("INSERT INTO "+orders+" (id) VALUES (?)", tc.order).Error; err != nil {
				return err
			}
			if _, err := box.Write(ctx, outbox.Message{ID: "order-created:" + tc.order + ":" + suffix, Topic: "orders", Key: []byte(tc.order), Body: []byte(tc.order)}); err != nil {
				return err
			}
			if tc.fail {
				return errors.New("rollback")
			}
			return nil
		})
		if tc.fail != (err != nil) {
			t.Fatalf("%s: %v", tc.order, err)
		}
	}
	allow := true
	factory, err := kafka.NewClientFactory(obs.Logger, testconfig.New(t, "kafka", &config_pb.Kafka{
		Connections: map[string]*config_pb.KafkaConnection{"external": {Brokers: []string{address}, AllowAutoTopicCreation: &allow}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	topic := "foundation_outbox_" + suffix
	raw, cleanupProducer, err := kafka.NewProducer(factory, kafka.ProducerConfig{Connection: "external", Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProducer()
	producer, err := kafka.NewManagedProducer(topic, raw, kafka.Observability{Logger: obs.Logger, Tracing: obs.Tracing, Metrics: obs.Metrics})
	if err != nil {
		t.Fatal(err)
	}
	stop := runRelay(t, box, outbox.RelayConfig{Producers: map[string]kafka.Producer{"orders": producer}})
	defer stop()
	consumer, err := kafka.NewConsumer(factory, obs.Logger, kafka.ConsumerConfig{Connection: "external", Topic: topic, Group: topic, Instance: topic, StartPosition: kafka.StartEarliest})
	if err != nil {
		t.Fatal(err)
	}
	received := errors.New("received")
	var message *kafka.Message
	if err := consumer.Consume(ctx, func(_ context.Context, d kafka.Delivery) error { message = d.Message; return received }); !errors.Is(err, received) {
		t.Fatal(err)
	}
	if message == nil || message.ID != "order-created:committed:"+suffix || string(message.Body) != "committed" {
		t.Fatalf("consumed = %+v", message)
	}
}
