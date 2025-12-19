package tracing

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.Tracing

var defaultCompression = kratos_foundation_pb.Tracing_Exporter_NO
var defaultSample = kratos_foundation_pb.Tracing_Sampler_RATIO

var defaultConfig = &Config{
	Disable:    proto.Bool(false),
	TracerName: proto.String(""),
	Exporter: &kratos_foundation_pb.Tracing_Exporter{
		EndpointUrl: proto.String("http://localhost:4318/v1/traces"),
		Compression: &defaultCompression,
		Headers:     nil,
		Timeout:     durationpb.New(10 * time.Second),
		Retry: &kratos_foundation_pb.Tracing_Exporter_RetryConfig{
			Enabled:         proto.Bool(true),
			InitialInterval: durationpb.New(5 * time.Second),
			MaxInterval:     durationpb.New(30 * time.Second),
			MaxElapsedTime:  durationpb.New(time.Minute),
		},
	},
	Sampler: &kratos_foundation_pb.Tracing_Sampler{
		Sample: &defaultSample,
		Ratio:  proto.Float64(0.05),
	},
}

func NewConfig(conf *kratos_foundation_pb.Config, appInfo *app_info.AppInfo) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetTracing())

	if c.GetTracerName() == "" {
		c.TracerName = proto.String(appInfo.GetName())
	}
	return c
}
