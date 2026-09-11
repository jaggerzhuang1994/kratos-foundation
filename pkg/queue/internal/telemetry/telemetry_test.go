package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type testTracingProvider struct{ provider trace.TracerProvider }

func (p testTracingProvider) Disabled() bool                       { return false }
func (p testTracingProvider) TracerProvider() trace.TracerProvider { return p.provider }
func (p testTracingProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

func newTelemetryForTest(t *testing.T) (*Telemetry, *tracetest.InMemoryExporter, metrics.Provider) {
	t.Helper()
	metricProvider, cleanup, err := metrics.NewProvider(appinfo.New("telemetry-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exporter))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	value, err := New(testTracingProvider{provider: tracerProvider}, metricProvider)
	if err != nil {
		t.Fatal(err)
	}
	return value, exporter, metricProvider
}

type telemetryMetricSample struct {
	labels         map[string]string
	counter        float64
	histogramCount uint64
	histogramSum   float64
}

func findTelemetryMetricSample(
	t testing.TB,
	provider metrics.Provider,
	familyName string,
	wantLabels map[string]string,
) telemetryMetricSample {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			matches := true
			for key, value := range wantLabels {
				if labels[key] != value {
					matches = false
					break
				}
			}
			if matches {
				return telemetryMetricSample{
					labels:         labels,
					counter:        metric.GetCounter().GetValue(),
					histogramCount: metric.GetHistogram().GetSampleCount(),
					histogramSum:   metric.GetHistogram().GetSampleSum(),
				}
			}
		}
	}
	t.Fatalf("metric family %q does not contain labels %#v", familyName, wantLabels)
	return telemetryMetricSample{}
}

func TestTelemetryRecordsAttemptOutcomesAndFinalClassifications(t *testing.T) {
	telemetry, exporter, metricProvider := newTelemetryForTest(t)
	ctx, span := telemetry.Tracer().Start(context.Background(), "consume")
	telemetry.RecordAttempt(ctx, span, "orders", "worker", 1, nil, time.Second)
	telemetry.RecordAttempt(ctx, span, "orders", "worker", 2, errors.New("retry"), 2*time.Second)
	telemetry.RecordFinalClassification(span, "permanent", 2)
	telemetry.RecordFinalClassification(span, "unsupported", 2)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	var queueEvents []string
	for _, event := range spans[0].Events {
		if len(event.Name) >= len("queue.") && event.Name[:len("queue.")] == "queue." {
			queueEvents = append(queueEvents, event.Name)
		}
	}
	if len(queueEvents) != 3 || queueEvents[0] != "queue.consume.attempt" || queueEvents[1] != "queue.consume.attempt" || queueEvents[2] != "queue.consume.permanent" {
		t.Fatalf("queue event names = %#v, want two attempts and permanent classification", queueEvents)
	}
	for _, result := range []string{"success", "error"} {
		labels := map[string]string{
			"queue_destination": "orders",
			"queue_consumer":    "worker",
			"queue_result":      result,
		}
		if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_attempts_total", labels); sample.counter != 1 {
			t.Fatalf("%s attempt counter = %v, want 1", result, sample.counter)
		}
		wantDuration := 1.0
		if result == "error" {
			wantDuration = 2.0
		}
		if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_attempt_duration_seconds", labels); sample.histogramCount != 1 || sample.histogramSum != wantDuration {
			t.Fatalf("%s attempt duration = count %d sum %v, want count 1 sum %v", result, sample.histogramCount, sample.histogramSum, wantDuration)
		}
	}
}

func TestTelemetryRecordsTaskRetryFailureRuntimeAndDispatcher(t *testing.T) {
	telemetry, exporter, metricProvider := newTelemetryForTest(t)
	ctx, span := telemetry.Tracer().Start(context.Background(), "consume")
	telemetry.RecordMessage(ctx, "orders", "worker", "success", time.Millisecond)
	telemetry.RecordRetry(ctx, span, "orders", "worker", 3)
	telemetry.RecordFailure(ctx, span, "orders", "worker", "success")
	telemetry.RecordFailure(ctx, span, "orders", "worker", "error")
	telemetry.RecordRuntimeFailure(ctx, "orders", "worker")
	telemetry.RecordProducer(ctx, "orders", "publish", "success", 2, time.Second)
	span.End()

	events := exporter.GetSpans()[0].Events
	if len(events) != 3 || events[0].Name != "queue.consume.retry" || events[1].Name != "queue.task.failed" || events[2].Name != "queue.failure.persist_failed" {
		t.Fatalf("unexpected telemetry events: %#v", events)
	}
	consumerLabels := map[string]string{
		"queue_destination": "orders",
		"queue_consumer":    "worker",
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_messages_total", map[string]string{
		"queue_destination": "orders", "queue_consumer": "worker", "queue_result": "success",
	}); sample.counter != 1 {
		t.Fatalf("message counter = %v, want 1", sample.counter)
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_message_duration_seconds", map[string]string{
		"queue_destination": "orders", "queue_consumer": "worker", "queue_result": "success",
	}); sample.histogramCount != 1 || sample.histogramSum != 0.001 {
		t.Fatalf("message duration = count %d sum %v, want count 1 sum 0.001", sample.histogramCount, sample.histogramSum)
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_retries_total", map[string]string{
		"queue_destination": consumerLabels["queue_destination"], "queue_consumer": consumerLabels["queue_consumer"], "queue_result": "scheduled",
	}); sample.counter != 1 {
		t.Fatalf("retry counter = %v, want 1", sample.counter)
	}
	for _, result := range []string{"success", "error"} {
		if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_failed_tasks_total", map[string]string{
			"queue_destination": "orders", "queue_consumer": "worker", "queue_result": result,
		}); sample.counter != 1 {
			t.Fatalf("%s failed-task counter = %v, want 1", result, sample.counter)
		}
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_consumer_runtime_failures_total", map[string]string{
		"queue_destination": "orders", "queue_consumer": "worker", "queue_result": "error",
	}); sample.counter != 1 {
		t.Fatalf("runtime failure counter = %v, want 1", sample.counter)
	}
	producerLabels := map[string]string{
		"queue_destination": "orders", "queue_operation": "publish", "queue_result": "success",
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_producer_messages_total", producerLabels); sample.counter != 2 {
		t.Fatalf("producer counter = %v, want 2", sample.counter)
	}
	if sample := findTelemetryMetricSample(t, metricProvider, "queue_producer_duration_seconds", producerLabels); sample.histogramCount != 1 || sample.histogramSum != 1 {
		t.Fatalf("producer duration = count %d sum %v, want count 1 sum 1", sample.histogramCount, sample.histogramSum)
	}
}
