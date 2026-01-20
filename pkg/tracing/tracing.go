package tracing

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Tracer trace.Tracer

type Tracing interface {
	GetTracerName() string
	GetTracerProvider() TracerProvider
	GetTracer() Tracer
	GetServiceAttributes() []attribute.KeyValue
	Simple(ctx context.Context, spanName string, logic func(context.Context) error)
	Trace(ctx context.Context, spanName string, logic func(context.Context, trace.Span) error)
}

type tracing struct {
	config            Config
	tp                TracerProvider
	tracer            Tracer
	serviceAttributes app_info.ServiceAttributes
}

func NewTracing(
	config Config,
	tp TracerProvider,
	serviceAttributes app_info.ServiceAttributes,
) Tracing {
	return &tracing{
		config:            config,
		tp:                tp,
		tracer:            tp.Tracer(config.GetTracerName(), trace.WithInstrumentationAttributes(serviceAttributes...)),
		serviceAttributes: serviceAttributes,
	}
}

func (t *tracing) GetTracerName() string {
	return t.config.GetTracerName()
}

func (t *tracing) GetTracerProvider() TracerProvider {
	return t.tp
}

func (t *tracing) GetTracer() Tracer {
	return t.tracer
}

func (t *tracing) GetServiceAttributes() []attribute.KeyValue {
	return t.serviceAttributes
}

func (t *tracing) Simple(ctx context.Context, spanName string, logic func(context.Context) error) {
	t.Trace(ctx, spanName, func(ctx context.Context, _ trace.Span) error {
		return logic(ctx)
	})
}

func (t *tracing) Trace(ctx context.Context, spanName string, logic func(context.Context, trace.Span) error) {
	ctx, span := t.tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindInternal))
	var err error
	defer func() {
		if !span.IsRecording() {
			return
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	err = logic(ctx, span)
}
