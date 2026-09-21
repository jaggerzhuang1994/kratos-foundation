package tracing

import (
	"context"
	"runtime"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracer(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	_ = NewTracer()
	tracer := NewTracer("utils-test", "ignored")
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := tracer.StartForFunc(context.Background())
	if !span.SpanContext().IsValid() {
		t.Fatal("no span context")
	}
	tracer.Span(ctx, "child", func(context.Context) {})
	span.End()
	func() {
		defer func() {
			if got := recover(); got != "failure" {
				t.Errorf("unexpected panic: %v", got)
			}
		}()
		tracer.Span(context.Background(), "panic", func(context.Context) { panic("failure") })
	}()
	spans := recorder.Ended()
	if len(spans) != 3 || spans[0].Name() != "child" || spans[1].Name() != runtime.FuncForPC(pc).Name() ||
		spans[2].Name() != "panic" || spans[0].Parent().SpanID() != spans[1].SpanContext().SpanID() ||
		spans[1].InstrumentationScope().Name != "utils-test" {
		t.Fatal(spans)
	}
}
