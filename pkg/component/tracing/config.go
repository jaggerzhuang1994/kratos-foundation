package tracing

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.TracingComponentConfig_Tracing

var defaultConfig = &kratos_foundation_pb.TracingComponentConfig_Tracing{
	Disable:    false,
	TracerName: "",
	Exporter: &kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter{
		EndpointUrl: "http://localhost:4318/v1/traces",
		Compression: kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter_NO,
		Headers:     nil,
		Timeout:     durationpb.New(10 * time.Second),
		Retry: &kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter_RetryConfig{
			Enabled:         true,
			InitialInterval: durationpb.New(5 * time.Second),
			MaxInterval:     durationpb.New(30 * time.Second),
			MaxElapsedTime:  durationpb.New(time.Minute),
		},
	},
	Sampler: &kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler{
		Sample: kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_RATIO,
		Ratio:  0.05,
	},
}

func NewConfig(cfg config.Config, appInfo *kratos_foundation_pb.AppInfo) (*Config, error) {
	var scc kratos_foundation_pb.TracingComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan TracingComponentConfig failed")
	}

	tracingConfig := proto.CloneOf(defaultConfig)
	proto.Merge(tracingConfig, scc.GetTracing())

	if tracingConfig.GetTracerName() == "" {
		tracingConfig.TracerName = appInfo.GetName()
	}

	return tracingConfig, nil
}
