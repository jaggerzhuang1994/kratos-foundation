package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	queueredis "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/v9"
)

const (
	taskQueue       = "components-tasks"
	backlogQueue    = "components-backlog"
	emailQueue      = "components-email"
	reportQueue     = "components-report"
	eventTopic      = "components-events"
	deadLetterTopic = "components-deadletter"
	consumerName    = "components-demo"
)

var errDemoFailure = errors.New("demo handler failure")

// messaging 借用 Redis 和观测 Provider，独占两个 Kafka Producer。
// app 先停止运行时，再执行 cleanup 注销采样并释放 Producer，最后才能释放借用资源。
type messaging struct {
	producer kafka.Producer
	consumer *kafka.ConsumerRuntime
	worker   *queue.Worker[string]
	tasks    *queue.Queue[string]
	backlog  *queue.Queue[string]
	business []businessQueue
	client   *redis.Client
	logger   log.Logger
}

// businessQueue 保存固定业务类型及其运行时；资源随 messaging 一起启动和停止。
type businessQueue struct {
	name     string
	taskType string
	payload  string
	tasks    *queue.Queue[string]
	worker   *queue.Worker[string]
}

func newMessaging(manager foundationredis.Manager, factory *kafka.ClientFactory, metricsProvider metrics.Provider, tracingProvider tracing.Provider, logger log.Logger) (_ *messaging, cleanup func(), err error) {
	var releases []func()
	cleanup = func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	logger = logger.WithModule("messaging")
	m := &messaging{logger: logger, business: []businessQueue{
		{name: emailQueue, taskType: "email.render", payload: "welcome"},
		{name: reportQueue, taskType: "report.summarize", payload: "daily"},
	}}
	m.client, err = manager.Connection("main")
	if err != nil {
		return nil, cleanup, err
	}
	kafkaObs := kafka.Observability{Logger: logger, Metrics: metricsProvider, Tracing: tracingProvider}
	producers := make([]kafka.Producer, 0, 2)
	for _, topic := range []string{eventTopic, deadLetterTopic} {
		raw, release, createErr := kafka.NewProducer(factory, kafka.ProducerConfig{Connection: "main", Topic: topic})
		if createErr != nil {
			return nil, cleanup, createErr
		}
		releases = append(releases, release)
		managed, createErr := kafka.NewManagedProducer(topic, raw, kafkaObs)
		if createErr != nil {
			return nil, cleanup, createErr
		}
		producers = append(producers, managed)
	}
	m.producer = producers[0]
	rawConsumer, err := kafka.NewConsumer(factory, logger, kafka.ConsumerConfig{
		Connection: "main", Topic: eventTopic, Group: consumerName, Instance: "components-api", Concurrency: 1,
	})
	if err != nil {
		return nil, cleanup, err
	}
	m.consumer, err = kafka.NewConsumerRuntime(kafka.RuntimeConfig{
		Name: consumerName, Destination: eventTopic,
		Retry:      &kafka.RetryPolicy{MaxAttempts: 2, MinBackoff: 100 * time.Millisecond, MaxBackoff: 100 * time.Millisecond},
		DeadLetter: producers[1], DeadLetterDestination: deadLetterTopic,
	}, rawConsumer, m.handleEvent, kafkaObs)
	if err != nil {
		return nil, cleanup, err
	}
	queueObs := queue.Observability{Logger: logger, Metrics: metricsProvider, Tracing: tracingProvider}
	for _, name := range []string{taskQueue, backlogQueue, emailQueue, reportQueue} {
		store, createErr := queueredis.NewStore(manager, queueredis.Config{Connection: "main", KeyPrefix: name})
		if createErr != nil {
			return nil, cleanup, createErr
		}
		unregister, createErr := queue.RegisterStats(name, store, metricsProvider, time.Second)
		if createErr != nil {
			return nil, cleanup, createErr
		}
		releases = append(releases, func() {
			if releaseErr := unregister(); releaseErr != nil {
				logger.With("queue", name, "error", releaseErr).Error("Failed to unregister queue statistics")
			}
		})
		workerName, taskType, handler := consumerName, "demo", m.handleTask
		switch name {
		case emailQueue:
			workerName, taskType, handler = "email-worker", "email.render", m.handleEmail
		case reportQueue:
			workerName, taskType, handler = "report-worker", "report.summarize", m.handleReport
		}
		q, createErr := queue.NewQueue(queue.Definition[string]{Queue: name, MessageType: taskType, Version: 1, Codec: taskTextCodec{}}, store, queueObs)
		if createErr != nil {
			return nil, cleanup, createErr
		}
		if name == backlogQueue {
			m.backlog = q
			continue
		}
		worker, createErr := q.WorkerWithExecution(func(ctx context.Context, execution queue.Execution[string]) error {
			return handler(ctx, &queue.Task{ID: execution.ID, Payload: []byte(execution.Message)})
		}, queue.WorkerConfig{
			Name: workerName, Concurrency: 1, PollInterval: 100 * time.Millisecond,
			Retry: &queue.RetryPolicy{MaxAttempts: 2, MinBackoff: 100 * time.Millisecond, MaxBackoff: 100 * time.Millisecond},
		})
		if createErr != nil {
			return nil, cleanup, createErr
		}
		if name == taskQueue {
			m.tasks, m.worker = q, worker
			continue
		}
		for i := range m.business {
			if m.business[i].name == name {
				m.business[i].tasks, m.business[i].worker = q, worker
			}
		}
	}
	return m, cleanup, nil
}

// Register 在业务声明阶段登记，启动与停止由 app supervisor 统一负责。
func (m *messaging) Register(spec *bootstrap.Spec) {
	spec.RegisterKafkaConsumer(m.consumer).RegisterRuntime(m.worker)
	for _, business := range m.business {
		spec.RegisterRuntime(business.worker)
	}
}

// Run 每轮投递三种消费场景、两项无 Worker 的积压任务，以及邮件和报表各一项任务；ID 由调用方生成的 UUID 隔离。
// 投递不具备跨 Redis/Kafka 原子性；失败时返回错误，不自动重发已成功的部分。
func (m *messaging) Run(ctx context.Context, runID string) error {
	for _, scenario := range []string{"success", "retry", "permanent"} {
		id := runID + "-" + scenario
		if err := m.producer.Publish(ctx, &kafka.Message{ID: id, Key: []byte(runID), Body: []byte(scenario)}); err != nil {
			return fmt.Errorf("publish demo event: %w", err)
		}
		if _, err := m.tasks.PostWith(ctx, scenario, queue.PostOptions{ID: id}); err != nil {
			return fmt.Errorf("dispatch demo task: %w", err)
		}
	}
	for _, scenario := range []string{"ready", "scheduled"} {
		available := time.Now().UTC()
		if scenario == "scheduled" {
			available = available.Add(24 * time.Hour)
		}
		if _, err := m.backlog.PostWith(ctx, "success", queue.PostOptions{ID: runID + "-" + scenario, AvailableAt: available}); err != nil {
			return fmt.Errorf("dispatch demo backlog: %w", err)
		}
	}
	for _, business := range m.business {
		if _, err := business.tasks.PostWith(ctx, business.payload, queue.PostOptions{ID: runID + "-" + business.taskType}); err != nil {
			return fmt.Errorf("dispatch %s task: %w", business.name, err)
		}
	}
	m.logger.WithContext(ctx).Infow("event", "messages.dispatched", "run_id", runID)
	return nil
}

func (m *messaging) handleEvent(ctx context.Context, message *kafka.Message) error {
	if string(message.Body) == "permanent" {
		return kafka.Permanent(errDemoFailure)
	}
	return m.handleScenario(ctx, "kafka", message.ID, string(message.Body))
}

func (m *messaging) handleTask(ctx context.Context, task *queue.Task) error {
	if string(task.Payload) == "permanent" {
		return queue.Permanent(errDemoFailure)
	}
	return m.handleScenario(ctx, "queue", task.ID, string(task.Payload))
}

func (m *messaging) handleScenario(ctx context.Context, backend, id, scenario string) error {
	switch scenario {
	case "success":
		return nil
	case "retry":
		// 每个后端/消息一个 Redis 原子计数，TTL 与 INCR 同一脚本提交，避免中断遗留永久键。
		// 不保存 Go 共享计数；运行时重试/租约策略保持组件原样。一天后重放可能再次演示失败。
		attempt, err := m.client.Eval(ctx, `local n = redis.call('INCR', KEYS[1]); redis.call('EXPIRE', KEYS[1], 86400); return n`, []string{"components:attempt:" + backend + ":" + id}).Int64()
		if err != nil {
			return fmt.Errorf("count demo attempt: %w", err)
		}
		if attempt == 1 {
			return errDemoFailure
		}
		return nil
	default:
		return fmt.Errorf("unsupported demo scenario %q", scenario)
	}
}

// handleEmail 执行本地欢迎邮件模板渲染，不依赖外部邮件网关。
func (m *messaging) handleEmail(ctx context.Context, task *queue.Task) error {
	if string(task.Payload) != "welcome" {
		return queue.Permanent(errors.New("unsupported email template"))
	}
	body := fmt.Sprintf("Welcome! Your request %s is ready.", task.ID)
	m.logger.WithContext(ctx).Infow("event", "email.rendered", "task_id", task.ID, "bytes", len(body))
	return nil
}

// handleReport 汇总固定演示数据，消费成功由真实 Worker 确认并产生指标。
func (m *messaging) handleReport(ctx context.Context, task *queue.Task) error {
	if string(task.Payload) != "daily" {
		return queue.Permanent(errors.New("unsupported report period"))
	}
	total := 0
	for _, amount := range []int{120, 80, 200} {
		total += amount
	}
	m.logger.WithContext(ctx).Infow("event", "report.summarized", "task_id", task.ID, "total", total)
	return nil
}

// taskTextCodec 保留演示任务的纯文本格式；任务类型显式带版本。
type taskTextCodec struct{}

func (taskTextCodec) Encode(value string) ([]byte, error) { return []byte(value), nil }
func (taskTextCodec) Decode(value []byte) (string, error) { return string(value), nil }
