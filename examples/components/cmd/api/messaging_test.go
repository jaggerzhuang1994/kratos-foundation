package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestMessagingHandlers(t *testing.T) {
	m := &messaging{}
	for _, scenario := range []string{"success", "permanent", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			eventErr := m.handleEvent(context.Background(), &kafka.Message{Body: []byte(scenario)})
			taskErr := m.handleTask(context.Background(), &queue.Task{Payload: []byte(scenario)})
			switch scenario {
			case "success":
				if eventErr != nil || taskErr != nil {
					t.Fatalf("success errors: %v, %v", eventErr, taskErr)
				}
			case "permanent":
				if !kafka.IsPermanent(eventErr) || !queue.IsPermanent(taskErr) {
					t.Fatalf("missing permanent classification: %v, %v", eventErr, taskErr)
				}
			default:
				if eventErr == nil || taskErr == nil {
					t.Fatal("unknown scenario accepted")
				}
			}
		})
	}
}

// TestMessagingRetry 使用显式测试 Redis 验证 Lua 计数、后端隔离和清理；默认不依赖外部服务。
func TestMessagingRetry(t *testing.T) {
	addr := os.Getenv("FOUNDATION_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FOUNDATION_TEST_REDIS_ADDR for real Redis retry verification")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, ContextTimeoutEnabled: true})
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	m := &messaging{client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id := uuid.NewString()
	keys := []string{"components:attempt:kafka:" + id, "components:attempt:queue:" + id}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		if err := client.Del(cleanupCtx, keys...).Err(); err != nil {
			t.Error(err)
		}
	}()
	for _, backend := range []string{"kafka", "queue"} {
		if err := m.handleScenario(ctx, backend, id, "retry"); !errors.Is(err, errDemoFailure) {
			t.Fatalf("first %s attempt: %v", backend, err)
		}
		if err := m.handleScenario(ctx, backend, id, "retry"); err != nil {
			t.Fatalf("second %s attempt: %v", backend, err)
		}
		ttl, err := client.TTL(ctx, "components:attempt:"+backend+":"+id).Result()
		if err != nil || ttl <= 0 || ttl > 24*time.Hour {
			t.Fatalf("ttl = %v, error = %v", ttl, err)
		}
	}
}

// 外部边界替身仅记录最终投递数据，断言每轮业务数量与隔离，不复刻组件重试实现。
type messagingProducer struct {
	kafka.Producer
	messages []*kafka.Message
	err      error
}

func (p *messagingProducer) Publish(_ context.Context, msg *kafka.Message) error {
	if p.err != nil {
		return p.err
	}
	p.messages = append(p.messages, msg.Clone())
	return nil
}

type messagingStore struct {
	queue.Store
	tasks []*queue.Task
	err   error
}

func (s *messagingStore) Enqueue(_ context.Context, task *queue.Task) error {
	if s.err != nil {
		return s.err
	}
	s.tasks = append(s.tasks, task.Clone())
	return nil
}

type messagingLogger struct{ log.Logger }

func (l messagingLogger) WithModule(string) log.Logger { return l }

func (l messagingLogger) WithContext(context.Context) log.Logger { return l }
func (messagingLogger) Infow(...any)                             {}
func (messagingLogger) Errorw(...any)                            {}

type messagingMetrics struct{ metrics.Provider }

func (messagingMetrics) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return metricnoop.NewMeterProvider().Meter(name, opts...)
}

type messagingTracing struct{ tracing.Provider }

func (messagingTracing) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return tracenoop.NewTracerProvider().Tracer(name, opts...)
}

func TestMessagingRun(t *testing.T) {
	for _, failure := range []string{"", "publish", "dispatch", "backlog", "email", "report"} {
		t.Run(failure, func(t *testing.T) {
			producer := &messagingProducer{}
			tasks, backlog := &messagingStore{}, &messagingStore{}
			emails, reports := &messagingStore{}, &messagingStore{}
			switch failure {
			case "publish":
				producer.err = errDemoFailure
			case "dispatch":
				tasks.err = errDemoFailure
			case "backlog":
				backlog.err = errDemoFailure
			case "email":
				emails.err = errDemoFailure
			case "report":
				reports.err = errDemoFailure
			}
			obs := queue.Observability{Logger: messagingLogger{}, Metrics: messagingMetrics{}, Tracing: messagingTracing{}}
			dispatcher, err := queue.NewQueue(queue.Definition[string]{Queue: taskQueue, MessageType: "demo", Version: 1, Codec: taskTextCodec{}}, tasks, obs)
			if err != nil {
				t.Fatal(err)
			}
			backlogDispatcher, err := queue.NewQueue(queue.Definition[string]{Queue: backlogQueue, MessageType: "demo", Version: 1, Codec: taskTextCodec{}}, backlog, obs)
			if err != nil {
				t.Fatal(err)
			}
			m := &messaging{producer: producer, tasks: dispatcher, backlog: backlogDispatcher, logger: obs.Logger}
			for _, business := range []struct {
				name, taskType, payload string
				store                   *messagingStore
			}{
				{emailQueue, "email.render", "welcome", emails},
				{reportQueue, "report.summarize", "daily", reports},
			} {
				dispatcher, createErr := queue.NewQueue(queue.Definition[string]{Queue: business.name, MessageType: business.taskType, Version: 1, Codec: taskTextCodec{}}, business.store, obs)
				if createErr != nil {
					t.Fatal(createErr)
				}
				m.business = append(m.business, businessQueue{name: business.name, taskType: business.taskType, payload: business.payload, tasks: dispatcher})
			}
			err = m.Run(context.Background(), "run-one")
			if failure != "" {
				if !errors.Is(err, errDemoFailure) {
					t.Fatalf("error = %v", err)
				}
				wantBacklog, wantEmail := 0, 0
				if failure == "email" || failure == "report" {
					wantBacklog = 2
				}
				if failure == "report" {
					wantEmail = 1
				}
				if len(backlog.tasks) != wantBacklog || len(emails.tasks) != wantEmail || len(reports.tasks) != 0 {
					t.Fatal("continued after failure or lost earlier dispatches")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(producer.messages) != 3 || len(tasks.tasks) != 3 || len(backlog.tasks) != 2 {
				t.Fatalf("counts = %d/%d/%d", len(producer.messages), len(tasks.tasks), len(backlog.tasks))
			}
			for i, scenario := range []string{"success", "retry", "permanent"} {
				if producer.messages[i].ID != "run-one-"+scenario || string(producer.messages[i].Body) != scenario || tasks.tasks[i].ID != producer.messages[i].ID || string(tasks.tasks[i].Payload) != scenario {
					t.Fatalf("scenario %s is not isolated", scenario)
				}
			}
			for i, store := range []*messagingStore{emails, reports} {
				business := m.business[i]
				if len(store.tasks) != 1 {
					t.Fatalf("%s tasks = %d", business.name, len(store.tasks))
				}
				task := store.tasks[0]
				if task.ID != "run-one-"+business.taskType || task.Type != business.taskType+".v1" || string(task.Payload) != business.payload {
					t.Fatalf("unexpected business task: %+v", task)
				}
			}
			delay := backlog.tasks[1].AvailableAt.Sub(backlog.tasks[0].AvailableAt)
			if delay < 24*time.Hour || delay > 24*time.Hour+time.Second {
				t.Fatalf("backlog delay = %v", delay)
			}
		})
	}
}

type messagingRedisManager struct{ foundationredis.Manager }

func (messagingRedisManager) Connection(string) (*redis.Client, error) { return nil, errDemoFailure }

func TestNewMessagingConnectionFailure(t *testing.T) {
	m, _, err := newMessaging(messagingRedisManager{}, nil, nil, nil, messagingLogger{})
	if m != nil || !errors.Is(err, errDemoFailure) {
		t.Fatalf("constructor = %v, %v", m, err)
	}
}

func TestMessagingBusinessHandlers(t *testing.T) {
	m := &messaging{logger: messagingLogger{}}
	for _, tc := range []struct {
		name, payload string
		handler       func(context.Context, *queue.Task) error
	}{
		{"email", "welcome", m.handleEmail},
		{"report", "daily", m.handleReport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.handler(context.Background(), &queue.Task{ID: "business-task", Payload: []byte(tc.payload)}); err != nil {
				t.Fatal(err)
			}
			if err := tc.handler(context.Background(), &queue.Task{Payload: []byte("invalid")}); !queue.IsPermanent(err) {
				t.Fatalf("invalid payload error = %v", err)
			}
		})
	}
}

func TestTaskTextCodec(t *testing.T) {
	codec := taskTextCodec{}
	encoded, err := codec.Encode("hello")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(encoded)
	if err != nil || decoded != "hello" {
		t.Fatalf("roundtrip = %q, %v", decoded, err)
	}
}
