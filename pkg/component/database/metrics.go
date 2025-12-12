package database

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/plugin/prometheus"
)

type MetricsPlugin = prometheus.Prometheus

func NewMetricsPlugin(
	c *Config,
	metrics *metrics.Metrics,
) *MetricsPlugin {
	if c.GetMetrics().GetDisable() {
		return nil
	}
	return prometheus.New(prometheus.Config{
		RefreshInterval: uint32(c.GetMetrics().GetRefreshInterval().GetSeconds()), // Refresh metrics interval (default 15 seconds)
		Labels:          mergeLabels(c.GetMetrics().GetLabels(), metrics.ServiceAttrs()),
		MetricsCollector: []prometheus.MetricsCollector{
			&prometheus.MySQL{
				Prefix:        c.GetMetrics().GetMysql().GetPrefix(),
				Interval:      uint32(c.GetMetrics().GetMysql().GetInterval().GetSeconds()),
				VariableNames: c.GetMetrics().GetMysql().GetVariableNames(),
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
