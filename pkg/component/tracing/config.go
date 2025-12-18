package tracing

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.TracingComponentConfig_Tracing

var defaultCompression = kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter_NO
var defaultSample = kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler_RATIO

var defaultConfig = &kratos_foundation_pb.TracingComponentConfig_Tracing{
	Disable:    proto.Bool(false),
	TracerName: proto.String(""),
	Exporter: &kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter{
		EndpointUrl: proto.String("http://localhost:4318/v1/traces"),
		Compression: &defaultCompression,
		Headers:     nil,
		Timeout:     durationpb.New(10 * time.Second),
		Retry: &kratos_foundation_pb.TracingComponentConfig_Tracing_Exporter_RetryConfig{
			Enabled:         proto.Bool(true),
			InitialInterval: durationpb.New(5 * time.Second),
			MaxInterval:     durationpb.New(30 * time.Second),
			MaxElapsedTime:  durationpb.New(time.Minute),
		},
	},
	Sampler: &kratos_foundation_pb.TracingComponentConfig_Tracing_Sampler{
		Sample: &defaultSample,
		Ratio:  proto.Float64(0.05),
	},
}

func NewConfig(cfg config.Config, appInfo *app_info.AppInfo) (*Config, error) {
	var scc kratos_foundation_pb.TracingComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan TracingComponentConfig failed")
	}

	tracingConfig := proto.CloneOf(defaultConfig)
	proto.Merge(tracingConfig, scc.GetTracing())

	if tracingConfig.GetTracerName() == "" {
		tracingConfig.TracerName = proto.String(appInfo.GetName())
	}

	return tracingConfig, nil
}
