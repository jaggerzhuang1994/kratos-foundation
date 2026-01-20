package tracing

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
)

type Exporter trace.SpanExporter

func NewExporter(config Config) (Exporter, error) {
	if config.GetDisable() {
		return nil, nil
	}

	var exporterConfig = config.GetExporter()
	var opts []otlptracehttp.Option

	if exporterConfig.GetEndpointUrl() != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(exporterConfig.GetEndpointUrl()))
	}

	if config != nil {
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.Compression(exporterConfig.GetCompression())))
	}

	if exporterConfig.GetHeaders() != nil {
		opts = append(opts, otlptracehttp.WithHeaders(exporterConfig.GetHeaders()))
	}

	if exporterConfig.GetTimeout().AsDuration() > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(exporterConfig.GetTimeout().AsDuration()))
	}

	if exporterConfig.GetRetry() != nil {
		opts = append(opts, otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         exporterConfig.GetRetry().GetEnabled(),
			InitialInterval: exporterConfig.GetRetry().GetInitialInterval().AsDuration(),
			MaxInterval:     exporterConfig.GetRetry().GetMaxInterval().AsDuration(),
			MaxElapsedTime:  exporterConfig.GetRetry().GetMaxElapsedTime().AsDuration(),
		}))
	}

	return otlptracehttp.New(context.Background(), opts...)
}
