package tracing

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type TracerProvider trace.TracerProvider

func NewTracerProvider(
	config Config,
	exporter Exporter,
	sampler Sampler,
	serviceAttributes app_info.ServiceAttributes,
) (TracerProvider, func()) {
	if config.GetDisable() {
		return noop.NewTracerProvider(), func() {
		}
	}
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSampler(sampler),
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(resource.NewSchemaless(
			serviceAttributes...,
		)),
	)

	return tp, func() {
		_ = tp.Shutdown(context.Background())
	}
}
