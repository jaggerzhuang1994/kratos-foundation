package telemetry

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"

// Telemetry 在对应能力启用时记录队列的 trace 和低基数 metrics。
type Telemetry struct {
	// tracer 创建队列投递与消费 Span。
	tracer trace.Tracer
	// consumerMessages 记录完成处理阶段的消息数量。
	consumerMessages metric.Int64Counter
	// consumerDuration 记录执行阶段耗时，含状态落库及失败通知，不含领取请求，单位为秒。
	consumerDuration metric.Float64Histogram
	// attempts 记录进入执行阶段的次数，含未调用 Handler 的超限或缺少处理器等情况。
	attempts metric.Int64Counter
	// attemptDuration 记录单次处理尝试耗时，单位为秒。
	attemptDuration metric.Float64Histogram
	// retries 记录已安排的重试次数。
	retries metric.Int64Counter
	// failedTasks 记录最终失败归档操作次数。
	failedTasks metric.Int64Counter
	// consumerRuntimeFailures 记录消费运行时故障次数。
	consumerRuntimeFailures metric.Int64Counter
	// producerMessages 按结果记录已尝试入队的消息数，不含准备任务时即失败的请求。
	producerMessages metric.Int64Counter
	// producerDuration 记录投递耗时，单位为秒。
	producerDuration metric.Float64Histogram
}

// New 在启动阶段创建固定指标，使命名错误能阻止应用以残缺观测能力启动。
func New(
	tracingProvider tracing.Provider,
	metricsProvider metrics.Provider,
) (*Telemetry, error) {
	result := &Telemetry{tracer: tracingProvider.Tracer(instrumentationName)}
	meter := metricsProvider.Meter(instrumentationName)
	var err error
	result.consumerMessages, err = meter.Int64Counter(
		"queue_consumer_messages_total",
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, err
	}
	result.consumerDuration, err = meter.Float64Histogram(
		"queue_consumer_message_duration_seconds",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	result.attempts, err = meter.Int64Counter(
		"queue_consumer_attempts_total",
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		return nil, err
	}
	result.attemptDuration, err = meter.Float64Histogram(
		"queue_consumer_attempt_duration_seconds",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	result.retries, err = meter.Int64Counter(
		"queue_consumer_retries_total",
		metric.WithUnit("{retry}"),
	)
	if err != nil {
		return nil, err
	}
	result.failedTasks, err = meter.Int64Counter(
		"queue_consumer_failed_tasks_total",
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, err
	}
	result.consumerRuntimeFailures, err = meter.Int64Counter(
		"queue_consumer_runtime_failures_total",
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, err
	}
	result.producerMessages, err = meter.Int64Counter(
		"queue_producer_messages_total",
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, err
	}
	result.producerDuration, err = meter.Float64Histogram(
		"queue_producer_duration_seconds",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Tracer 返回队列运行时使用的 tracer。
func (t *Telemetry) Tracer() trace.Tracer {
	return t.tracer
}

func (t *Telemetry) RecordMessage(
	ctx context.Context,
	destination string,
	consumer string,
	result string,
	duration time.Duration,
) {
	attributes := consumerMetricAttributes(destination, consumer, result)
	t.consumerMessages.Add(ctx, 1, attributes)
	t.consumerDuration.Record(ctx, duration.Seconds(), attributes)
}

func (t *Telemetry) RecordAttempt(
	ctx context.Context,
	span trace.Span,
	destination string,
	consumer string,
	attempt int,
	err error,
	duration time.Duration,
) {
	result := "success"
	if err != nil {
		result = "error"
		span.RecordError(err)
	}
	attributes := consumerMetricAttributes(destination, consumer, result)
	t.attempts.Add(ctx, 1, attributes)
	t.attemptDuration.Record(ctx, duration.Seconds(), attributes)
	span.AddEvent(
		"queue.consume.attempt",
		trace.WithAttributes(
			attribute.Int("queue.attempt", attempt),
			attribute.String("queue.result", result),
		),
	)
}

func (t *Telemetry) RecordRetry(
	ctx context.Context,
	span trace.Span,
	destination string,
	consumer string,
	attempt int,
) {
	t.retries.Add(ctx, 1, consumerMetricAttributes(destination, consumer, "scheduled"))
	span.AddEvent(
		"queue.consume.retry",
		trace.WithAttributes(attribute.Int("queue.attempt", attempt)),
	)
}

// RecordFinalClassification 只接受固定失败分类作为事件名，并且不附加原始错误文本。
func (t *Telemetry) RecordFinalClassification(
	span trace.Span,
	classification string,
	attempts int,
) {
	var event string
	switch classification {
	case "permanent":
		event = "queue.consume.permanent"
	case "retry_exhausted":
		event = "queue.consume.retry_exhausted"
	default:
		return
	}
	span.AddEvent(
		event,
		trace.WithAttributes(attribute.Int("queue.attempts", attempts)),
	)
}

func (t *Telemetry) RecordFailure(
	ctx context.Context,
	span trace.Span,
	destination string,
	consumer string,
	result string,
) {
	t.failedTasks.Add(ctx, 1, consumerMetricAttributes(destination, consumer, result))
	event := "queue.task.failed"
	if result != "success" {
		event = "queue.failure.persist_failed"
	}
	span.AddEvent(event)
}

func (t *Telemetry) RecordRuntimeFailure(ctx context.Context, destination, consumer string) {
	t.consumerRuntimeFailures.Add(
		ctx,
		1,
		consumerMetricAttributes(destination, consumer, "error"),
	)
}

// RecordProducer 记录一次生产者调用的结果和耗时。
func (t *Telemetry) RecordProducer(
	ctx context.Context,
	destination string,
	operation string,
	result string,
	count int64,
	duration time.Duration,
) {
	attributes := metric.WithAttributes(
		attribute.String("queue.destination", destination),
		attribute.String("queue.operation", operation),
		attribute.String("queue.result", result),
	)
	t.producerMessages.Add(ctx, count, attributes)
	t.producerDuration.Record(ctx, duration.Seconds(), attributes)
}

func consumerMetricAttributes(destination, consumer, result string) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("queue.destination", destination),
		attribute.String("queue.consumer", consumer),
		attribute.String("queue.result", result),
	)
}
