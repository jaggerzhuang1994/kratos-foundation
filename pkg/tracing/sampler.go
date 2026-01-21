package tracing

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel/sdk/trace"
)

type Sampler trace.Sampler

func NewSampler(
	config Config,
	log log.Log,
) Sampler {
	if config.GetDisable() {
		return nil
	}
	log = log.WithModule("tracing/sampler", config.GetLog())

	samplerConfig := config.GetSampler()
	switch samplerConfig.GetSample() {
	case config_pb.Sampler_RATIO:
		return trace.ParentBased(trace.TraceIDRatioBased(samplerConfig.GetRatio()))
	case config_pb.Sampler_ALWAYS:
		return trace.AlwaysSample()
	case config_pb.Sampler_NEVER:
		return trace.NeverSample()
	}

	log.Warn("tracing sampler fallback: never sample")
	return trace.NeverSample()
}
