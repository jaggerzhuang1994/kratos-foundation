package tracing

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
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

	sampler := newSampler(cfg.GetSampler())
	exporter, err := newSpanExporter(ctx, cfg.GetExporter())
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
	if defaultTracerName == "" {
		defaultTracerName = appInfo.GetName()
	}

	return &Tracing{
			Helper:            l,
			tp:                tp,
			defaultTracerName: defaultTracerName,
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

func newSpanExporter(ctx context.Context, cfg *kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter) (*otlptrace.Exporter, error) {
	var opts []otlptracehttp.Option

	if cfg.GetEndpoint() != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(cfg.GetEndpoint()))
	}

	if cfg.GetEndpointUrl() != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(cfg.GetEndpointUrl()))
	}

	if cfg != nil && cfg.Compression != nil {
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.Compression(cfg.GetCompression())))
	}

	if cfg.GetUrlPath() != "" {
		opts = append(opts, otlptracehttp.WithURLPath(cfg.GetUrlPath()))
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

func newSampler(cfg *kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler) tracesdk.Sampler {
	switch cfg.GetSample() {
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_Ratio:
		return tracesdk.ParentBased(tracesdk.TraceIDRatioBased(cfg.GetRatio()))
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_AlwaysOn:
		return tracesdk.AlwaysSample()
	case kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_AlwaysOff:
		return tracesdk.NeverSample()
	}
	return tracesdk.NeverSample()
}
