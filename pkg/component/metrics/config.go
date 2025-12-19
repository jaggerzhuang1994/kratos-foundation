package metrics

import (
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.Metrics

var defaultConfig = &Config{
	CounterMapSize:   proto.Int32(64),
	GaugeMapSize:     proto.Int32(64),
	HistogramMapSize: proto.Int32(64),
}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetMetrics())
	return c
}
