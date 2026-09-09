package tracing

import (
	"context"
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracing 是业务应用记录自定义 Span 的默认 OpenTelemetry Tracer。
type Tracing = trace.Tracer

// Provider 持有应用私有的 TracerProvider，并按代码作用域创建 Tracer。
type Provider interface {
	// Disabled 表示 tracing 是否被显式禁用。
	Disabled() bool
	// TracerProvider 返回当前实例使用的 provider。
	TracerProvider() trace.TracerProvider
	// Tracer 返回指定 instrumentation scope 的 Tracer。
	Tracer(name string, options ...trace.TracerOption) trace.Tracer
}

// Trace 执行一次完整 Span 生命周期，并原样返回业务回调错误。
func Trace(
	ctx context.Context,
	tracer trace.Tracer,
	spanName string,
	logic func(context.Context, trace.Span) error,
) (err error) {
	if logic == nil {
		return errors.New("tracing callback is nil")
	}
	ctx, span := tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindInternal))
	defer func() {
		if err != nil && span.IsRecording() {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	return logic(ctx, span)
}

// NewTracing 使用业务应用名创建默认 Tracer。
func NewTracing(provider Provider, appInfo appinfo.AppInfo) Tracing {
	return provider.Tracer(appInfo.Name())
}
