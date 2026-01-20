package metrics

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type Config = *config_pb.Metrics

type DefaultConfig Config

func NewDefaultConfig(appInfo app_info.AppInfo) DefaultConfig {
	return &config_pb.Metrics{
		MeterName:        proto.String(appInfo.GetName()),
		CounterMapSize:   proto.Int32(64),
		GaugeMapSize:     proto.Int32(64),
		HistogramMapSize: proto.Int32(64),
		Log:              nil,
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetMetrics())
	return c
}
