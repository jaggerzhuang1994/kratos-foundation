package metric

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.MetricComponentConfig_Metric

var defaultConfig = &Config{
	CounterMapSize:   64,
	GaugeMapSize:     64,
	HistogramMapSize: 64,
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.MetricComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan MetricComponentConfig failed")
	}

	metricConfig := proto.CloneOf(defaultConfig)
	proto.Merge(metricConfig, scc.GetMetric())

	return metricConfig, nil
}
