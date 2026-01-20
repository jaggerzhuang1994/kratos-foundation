package otel

import (
	"context"

	context2 "github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/context"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type TracingProvider interface {
	RecordStart(ctx context.Context) (context.Context, trace.Span)
	RecordEnd(_ context.Context, span trace.Span, err error)
}

type tracingProvider struct {
	tracer trace.Tracer
}

func NewTracingProvider(
	tracing tracing.Tracing,
	config Config,
) TracingProvider {
	provider := &tracingProvider{}
	if config.GetTracing().GetDisable() {
		provider.tracer = noop.NewTracerProvider().Tracer("")
	} else {
		provider.tracer = tracing.GetTracer()
	}
	return provider
}

func (tp *tracingProvider) RecordStart(ctx context.Context) (context.Context, trace.Span) {
	var span trace.Span
	ctx, span = tp.tracer.Start(
		ctx,
		context2.GetJobName(ctx),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	return ctx, span
}

func (tp *tracingProvider) RecordEnd(_ context.Context, span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}
	span.End()
}
