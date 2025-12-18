package metrics

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.MetricComponentConfig_Metrics

var defaultConfig = &Config{
	CounterMapSize:   proto.Int32(64),
	GaugeMapSize:     proto.Int32(64),
	HistogramMapSize: proto.Int32(64),
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.MetricComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan MetricComponentConfig failed")
	}

	metricsConfig := proto.CloneOf(defaultConfig)
	proto.Merge(metricsConfig, scc.GetMetrics())

	return metricsConfig, nil
}
