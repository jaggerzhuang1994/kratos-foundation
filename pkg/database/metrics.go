package database

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
	"gorm.io/plugin/prometheus"
)

type MetricsPlugin gorm.Plugin

func NewMetricsPlugin(
	config Config,
	serviceAttributes app_info.ServiceAttributes,
) MetricsPlugin {
	conf := config.GetMetrics()
	if conf.GetDisable() {
		return nil
	}
	return prometheus.New(prometheus.Config{
		RefreshInterval: uint32(conf.GetRefreshInterval().GetSeconds()), // Refresh metrics interval (default 15 seconds)
		Labels:          mergeLabels(conf.GetLabels(), serviceAttributes),
		MetricsCollector: []prometheus.MetricsCollector{
			&prometheus.MySQL{
				Prefix:        conf.GetMysql().GetPrefix(),
				Interval:      uint32(conf.GetMysql().GetInterval().GetSeconds()),
				VariableNames: conf.GetMysql().GetVariableNames(),
			},
		}, // user defined metrics
	})
}

func mergeLabels(configLabels map[string]string, appAttrs []attribute.KeyValue) map[string]string {
	copyMap := make(map[string]string, len(configLabels)+len(appAttrs))
	for k, v := range configLabels {
		copyMap[k] = v
	}
	for _, attr := range appAttrs {
		copyMap["otel_scope_"+string(attr.Key)] = attr.Value.AsString()
	}
	return copyMap
}
