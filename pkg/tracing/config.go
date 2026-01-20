package tracing

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.Tracing

type DefaultConfig Config

func NewDefaultConfig(
	appInfo app_info.AppInfo,
) DefaultConfig {
	defaultDisable := false
	if env.IsLocal() {
		defaultDisable = true
	}

	defaultSample := config_pb.Sampler_RATIO
	defaultCompression := config_pb.Exporter_NO

	return &config_pb.Tracing{
		Disable:    proto.Bool(defaultDisable),
		TracerName: proto.String(appInfo.GetName()),
		Exporter: &config_pb.Exporter{
			EndpointUrl: proto.String("http://localhost:4318/v1/traces"),
			Compression: &defaultCompression,
			Headers:     nil,
			Timeout:     durationpb.New(10 * time.Second),
			Retry: &config_pb.Exporter_RetryConfig{
				Enabled:         proto.Bool(true),
				InitialInterval: durationpb.New(5 * time.Second),
				MaxInterval:     durationpb.New(30 * time.Second),
				MaxElapsedTime:  durationpb.New(time.Minute),
			},
		},
		Sampler: &config_pb.Sampler{
			Sample: &defaultSample,
			Ratio:  proto.Float64(0.05),
		},
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetTracing())
	return c
}
