package job

import (
	"context"
	"time"

	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationtracing "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	instrumentationNameJob    = "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	metricLabelJob            = "job"
	metricLabelStatus         = "status"
	jobRunsInstrumentName     = "job_runs_total"
	jobDurationInstrumentName = "job_duration_seconds"
	jobRunningInstrumentName  = "job_running"
)

type jobMetrics interface {
	reportStart(context.Context)
	reportDone(context.Context, error, time.Duration)
}

type jobMetricsProvider struct {
	disabled           bool
	jobRunsTotal       metric.Int64Counter
	jobDurationSeconds metric.Float64Histogram
	jobRunning         metric.Int64UpDownCounter
}

func newJobMetricsProvider(provider foundationmetrics.Provider, enabled bool) (jobMetrics, error) {
	result := &jobMetricsProvider{disabled: true}
	if !enabled {
		return result, nil
	}
	result.disabled = false
	meter := provider.Meter(instrumentationNameJob)
	var err error
	result.jobRunsTotal, err = meter.Int64Counter(jobRunsInstrumentName, metric.WithUnit("{call}"))
	if err != nil {
		return nil, err
	}
	result.jobDurationSeconds, err = meter.Float64Histogram(
		jobDurationInstrumentName,
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 30, 60, 300, 600, 1800, 3600, 7200, 21600, 43200, 86400),
	)
	if err != nil {
		return nil, err
	}
	result.jobRunning, err = meter.Int64UpDownCounter(jobRunningInstrumentName)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (provider *jobMetricsProvider) reportStart(ctx context.Context) {
	if provider.disabled {
		return
	}
	name := JobNameFromContext(ctx)
	if name == "" {
		return
	}
	provider.jobRunning.Add(ctx, 1, metric.WithAttributes(attribute.String(metricLabelJob, name)))
}

func (provider *jobMetricsProvider) reportDone(ctx context.Context, err error, duration time.Duration) {
	if provider.disabled {
		return
	}
	name := JobNameFromContext(ctx)
	if name == "" {
		return
	}
	status := "success"
	if err != nil {
		status = "failure"
	}
	attributes := metric.WithAttributes(
		attribute.String(metricLabelJob, name),
		attribute.String(metricLabelStatus, status),
	)
	provider.jobRunsTotal.Add(ctx, 1, attributes)
	provider.jobDurationSeconds.Record(ctx, duration.Seconds(), attributes)
	provider.jobRunning.Add(ctx, -1, metric.WithAttributes(attribute.String(metricLabelJob, name)))
}

type jobTracing interface {
	recordStart(context.Context) (context.Context, trace.Span)
	recordEnd(trace.Span, error)
}

type jobTracingProvider struct {
	tracer trace.Tracer
}

func newJobTracingProvider(provider foundationtracing.Provider, enabled bool) jobTracing {
	result := &jobTracingProvider{}
	if !enabled || provider.Disabled() {
		result.tracer = noop.NewTracerProvider().Tracer(instrumentationNameJob)
	} else {
		result.tracer = provider.Tracer(instrumentationNameJob)
	}
	return result
}

func (provider *jobTracingProvider) recordStart(ctx context.Context) (context.Context, trace.Span) {
	return provider.tracer.Start(
		ctx,
		JobNameFromContext(ctx),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

func (*jobTracingProvider) recordEnd(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}
	span.End()
}
