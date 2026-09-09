package tracing

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestNewTracingReturnsTracerNamedAfterApp(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	provider := testProvider{tp: tp}

	tracer := NewTracing(provider, testAppInfo{name: "orders"})
	_, span := tracer.Start(context.Background(), "order.create")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	if got := spans[0].InstrumentationScope.Name; got != "orders" {
		t.Fatalf("default tracer scope name = %q, want orders", got)
	}
}

func TestTraceReturnsCallbackErrorAndMarksSpanFailed(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	wantErr := errors.New("create order")

	err := Trace(
		context.Background(),
		tp.Tracer("github.com/example/orders/internal/application"),
		"order.create",
		func(context.Context, trace.Span) error { return wantErr },
	)
	if err != wantErr {
		t.Fatalf("Trace() error = %v, want original callback error", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status = %v, want error", spans[0].Status.Code)
	}
}
