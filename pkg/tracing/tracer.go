package tracing

import (
	"context"
	"runtime"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 包装 OpenTelemetry Tracer，不拥有 provider 的生命周期。
type Tracer struct {
	trace.Tracer
}

// NewTracer 从全局 provider 获取 Tracer，只使用第一个可选名称。
func NewTracer(optionalName ...string) *Tracer {
	var name string
	if len(optionalName) > 0 {
		name = optionalName[0]
	}
	return &Tracer{
		otel.GetTracerProvider().Tracer(name),
	}
}

// StartForFunc 以直接调用者的完整 Go 函数名启动 Span，由调用方结束。
func (t *Tracer) StartForFunc(ctx context.Context, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	pc, _, _, _ := runtime.Caller(1)
	name := runtime.FuncForPC(pc).Name()
	return t.Start(ctx, name, opts...)
}

// Span 在回调执行期间创建 Span，回调返回或 panic 时结束 Span。
func (t *Tracer) Span(ctx context.Context, name string, fn func(context.Context), opts ...trace.SpanStartOption) {
	ctx, span := t.Start(ctx, name, opts...)
	defer span.End()
	fn(ctx)
}
