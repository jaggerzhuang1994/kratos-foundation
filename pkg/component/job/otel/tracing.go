package otel

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type TracingProvider struct {
	tracer trace.Tracer
}

func NewTracingProvider(
	tracing *tracing.Tracing,
	config *config.Config,
) (provider *TracingProvider) {
	provider = &TracingProvider{}
	if config.GetTracing().GetDisable() {
		provider.tracer = noop.NewTracerProvider().Tracer(config.GetTracing().GetTracerName())
	} else {
		provider.tracer = tracing.GetTracer(config.GetTracing().GetTracerName())
	}
	return
}

func (tp *TracingProvider) RecordStart(ctx context.Context) (context.Context, trace.Span) {
	var span trace.Span
	ctx, span = tp.tracer.Start(
		ctx,
		job.GetName(ctx),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	return ctx, span
}

func (tp *TracingProvider) RecordEnd(_ context.Context, span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}
	span.End()
}
