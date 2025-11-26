package tracing

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type Tracing struct {
	*log.Helper
	tp                trace.TracerProvider
	defaultTracerName string
	defaultTracer     trace.Tracer
}

const logModule = "tracing"

func NewTracing(cfg *Config, appInfo *kratos_foundation_pb.AppInfo, log *log.Log) (*Tracing, func(), error) {
	l := log.WithModule(logModule, cfg.GetLog()).NewHelper()

	if cfg.GetDisable() {
		l.Info("tracing disabled")
		tp := noop.NewTracerProvider()
		return &Tracing{Helper: l, tp: tp}, func() {}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())

	sampler := newSampler(l, cfg.GetSampler())
	exporter, err := newExporter(ctx, cfg.GetExporter())
	if err != nil {
		l.Error("failed to create span exporter", "error", err)
		cancel()
		return nil, nil, err
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSampler(sampler),
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(resource.NewSchemaless(
			semconv.ServiceNameKey.String(appInfo.GetName()),
			semconv.ServiceInstanceIDKey.String(appInfo.GetId()),
			semconv.ServiceVersionKey.String(appInfo.GetVersion()),
		)),
	)

	defaultTracerName := cfg.GetTracerName()

	return &Tracing{
			Helper:            l,
			tp:                tp,
			defaultTracerName: defaultTracerName,
			defaultTracer:     tp.Tracer(defaultTracerName),
		}, func() {
			defer cancel()
			err := tp.Shutdown(context.Background())
			if err != nil {
				l.Error("failed to shutdown tracing", "error", err)
			}
		}, nil
}

func (t *Tracing) GetTracerProvider() trace.TracerProvider {
	return t.tp
}

func (t *Tracing) GetDefaultTracerName() string {
	return t.defaultTracerName
}

func (t *Tracing) SimpleTrace(ctx context.Context, spanName string, logic func(context.Context)) {
	t.Trace(ctx, spanName, func(ctx context.Context, _ trace.Span) error {
		logic(ctx)
		return nil
	})
}

func (t *Tracing) Trace(ctx context.Context, spanName string, logic func(context.Context, trace.Span) error) {
	ctx, span := t.defaultTracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindInternal))
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

func newExporter(ctx context.Context, cfg *kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter) (*otlptrace.Exporter, error) {
	var opts []otlptracehttp.Option

	if cfg.GetEndpointUrl() != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(cfg.GetEndpointUrl()))
	}

	if cfg != nil {
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.Compression(cfg.GetCompression())))
	}

	if cfg.GetHeaders() != nil {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.GetHeaders()))
	}

	if cfg.GetTimeout().AsDuration() > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(cfg.GetTimeout().AsDuration()))
	}

	if cfg.GetRetry() != nil {
		opts = append(opts, otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         cfg.GetRetry().GetEnabled(),
			InitialInterval: cfg.GetRetry().GetInitialInterval().AsDuration(),
			MaxInterval:     cfg.GetRetry().GetMaxInterval().AsDuration(),
			MaxElapsedTime:  cfg.GetRetry().GetMaxElapsedTime().AsDuration(),
		}))
	}

	return otlptracehttp.New(ctx, opts...)
}

func newSampler(log *log.Helper, cfg *kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler) tracesdk.Sampler {
	switch cfg.GetSample() {
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_RATIO:
		log.Info("tracing ratio sample", cfg.GetRatio())
		return tracesdk.ParentBased(tracesdk.TraceIDRatioBased(cfg.GetRatio()))
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_ALWAYS:
		log.Info("tracing always sample")
		return tracesdk.AlwaysSample()
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_NEVER:
		log.Info("tracing never sample")
		return tracesdk.NeverSample()
	}
	log.Warn("fallback: tracing never sample")
	return tracesdk.NeverSample()
}
