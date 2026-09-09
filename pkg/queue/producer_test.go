package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"go.opentelemetry.io/otel/trace"
)

type producerLogContextKey struct{}

func TestProducerPreparesCopyAndInjectsTraceContext(t *testing.T) {
	raw := newRecordingProducer()
	observability, spans := newTestObservability(t)
	producer, err := NewProducer("orders.created", raw, observability)
	if err != nil {
		t.Fatal(err)
	}
	ctx, parent := observability.Tracing.Tracer("test").Start(context.Background(), "parent")
	message := &Message{Body: []byte("order")}
	if err := producer.Publish(ctx, message); err != nil {
		t.Fatal(err)
	}
	parent.End()
	if message.ID != "" || !message.Timestamp.IsZero() || len(message.Headers) != 0 {
		t.Fatalf("input message was mutated: %#v", message)
	}
	got := raw.single()
	if got.ID == "" || got.Timestamp.IsZero() || got.Timestamp.Location() != time.UTC {
		t.Fatalf("prepared metadata = %#v", got)
	}
	if headerValue(got.Headers, "traceparent") == "" {
		t.Fatal("traceparent header was not injected")
	}
	if countSpan(spans, "queue.publish") != 1 {
		t.Fatal("producer span was not recorded")
	}
}

func TestProducerBatchRejectsInvalidInputBeforePublishing(t *testing.T) {
	raw := newRecordingProducer()
	producer, err := NewProducer("orders.created", raw, newDisabledTestObservability(t))
	if err != nil {
		t.Fatal(err)
	}
	err = producer.PublishBatch(context.Background(), []*Message{{Body: []byte("valid")}, nil})
	if err == nil {
		t.Fatal("PublishBatch accepted nil message")
	}
	if raw.publishCount() != 0 {
		t.Fatal("PublishBatch sent a partial batch")
	}
}

func TestProducerPreservesDriverErrorAndUsesStableMetricLabels(t *testing.T) {
	wantErr := errors.New("broker unavailable")
	raw := &recordingProducer{publishErr: wantErr}
	observability := newMetricTestObservability(t)
	producer, err := NewProducer("orders.created", raw, observability)
	if err != nil {
		t.Fatal(err)
	}
	err = producer.Publish(context.Background(), &Message{ID: "high-cardinality-id"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Publish error = %v, want wrapped driver error", err)
	}
	labels := gatheredQueueMetricLabels(t, observability.Metrics, "queue_producer_messages_total")
	if labels["queue_destination"] != "orders.created" || labels["queue_result"] != "error" {
		t.Fatalf("metric labels = %#v", labels)
	}
	if _, exists := labels["message_id"]; exists {
		t.Fatal("metric contains message_id")
	}
}

func TestProducerFailureLogExcludesPayloadAndHeaderValues(t *testing.T) {
	raw := &recordingProducer{publishErr: errors.New("broker unavailable")}
	observability := newDisabledTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	producer, err := NewProducer("orders.created", raw, observability)
	if err != nil {
		t.Fatal(err)
	}
	_ = producer.Publish(context.Background(), &Message{
		Body:    []byte("body-secret"),
		Headers: []Header{{Key: "authorization", Value: []byte("header-secret")}},
	})
	written := logs.String()
	if !strings.Contains(written, "queue.publish.failed") {
		t.Fatalf("failure event missing from log %q", written)
	}
	if strings.Contains(written, "body-secret") || strings.Contains(written, "header-secret") {
		t.Fatalf("sensitive message data leaked to log %q", written)
	}
}

func TestProducerRejectionLogsWithCallerContextAtWarn(t *testing.T) {
	observability := newDisabledTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	producer, err := NewProducer("orders.created", newRecordingProducer(), observability)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), producerLogContextKey{}, "request-1")
	if err := producer.Publish(ctx, &Message{
		Body:    []byte("body-secret"),
		Headers: []Header{{Key: " ", Value: []byte("header-secret")}},
	}); err == nil {
		t.Fatal("Publish accepted invalid header")
	}
	loggedCtx := logs.lastContext()
	if loggedCtx == nil || loggedCtx.Value(producerLogContextKey{}) != "request-1" {
		t.Fatalf("logged context = %#v, want caller context", loggedCtx)
	}
	if level, ok := logs.lastLevel(); !ok || level != kratoslog.LevelWarn {
		t.Fatalf("log level = %v, %v; want WARN", level, ok)
	}
	if written := logs.String(); !strings.Contains(written, "queue.publish.rejected") ||
		strings.Contains(written, "body-secret") || strings.Contains(written, "header-secret") {
		t.Fatalf("unsafe rejection log %q", written)
	}
}

func TestProducerFailureLogsWithActiveProducerSpanContextAtError(t *testing.T) {
	observability, _ := newTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	producer, err := NewProducer(
		"orders.created",
		&recordingProducer{publishErr: errors.New("broker unavailable")},
		observability,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Publish(context.Background(), &Message{}); err == nil {
		t.Fatal("Publish unexpectedly succeeded")
	}
	loggedCtx := logs.lastContext()
	if loggedCtx == nil || !trace.SpanContextFromContext(loggedCtx).IsValid() {
		t.Fatalf("logged context = %#v, want active producer span", loggedCtx)
	}
	if level, ok := logs.lastLevel(); !ok || level != kratoslog.LevelError {
		t.Fatalf("log level = %v, %v; want ERROR", level, ok)
	}
	if written := logs.String(); !strings.Contains(written, "queue.publish.failed") {
		t.Fatalf("failure event missing from log %q", written)
	}
}
