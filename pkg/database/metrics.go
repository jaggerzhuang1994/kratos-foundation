package database

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/otelattr"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel/attribute"
)

var prometheusLabelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// metricsCollector 只管理数据库指标的注册与释放，不参与连接池生命周期。
type metricsCollector struct {
	// registerer 登记和注销当前实例指标的注册器。
	registerer prometheus.Registerer
	// dbStats 当前实例登记的连接池指标。
	dbStats []prometheus.Collector
	// sql 可选的 SQL 操作指标。
	sql *sqlMetrics
	// closeOnce 保证刷新器关闭和指标注销仅执行一次。
	closeOnce sync.Once
}

// newMetricsCollector 为所有具名连接注册应用侧连接池指标。
func newMetricsCollector(
	connectionFactory *connectionFactory,
	config *config_pb.Database,
	appInfo appinfo.AppInfo,
	metricsProvider foundationmetrics.Provider,
) (*metricsCollector, error) {
	conf := config.GetMetrics()
	if conf.GetDisable() {
		return nil, nil
	}
	baseRegisterer := metricsProvider.PrometheusRegisterer()
	if baseRegisterer == nil {
		return nil, errors.New("database metrics Prometheus registerer is nil")
	}
	labels, err := mergeMetricLabels(
		conf.GetLabels(),
		otelattr.ServiceAttributes(appInfo),
	)
	if err != nil {
		return nil, err
	}
	registerer := prometheus.WrapRegistererWith(labels, baseRegisterer)
	collector := &metricsCollector{registerer: registerer}

	for _, pool := range connectionFactory.pools() {
		dbStats := collectors.NewDBStatsCollector(pool.db, pool.name)
		if err := registerer.Register(dbStats); err != nil {
			collector.unregisterDBStats()
			return nil, fmt.Errorf(
				"register database pool metrics for %q: %w",
				pool.name,
				err,
			)
		}
		collector.dbStats = append(collector.dbStats, dbStats)
	}

	return collector, nil
}

// close 幂等地从私有 Registry 注销全部应用侧数据库指标。
func (c *metricsCollector) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.sql.unregister(c.registerer)
		c.unregisterDBStats()
	})
}

// unregisterDBStats 注销已成功注册的连接池指标，用于回滚部分构造。
func (c *metricsCollector) unregisterDBStats() {
	for _, collector := range c.dbStats {
		c.registerer.Unregister(collector)
	}
	c.dbStats = nil
}

// mergeMetricLabels 合并用户标签与应用身份，并拒绝隐式覆盖和保留标签。
func mergeMetricLabels(
	configLabels map[string]string,
	appAttrs []attribute.KeyValue,
) (map[string]string, error) {
	labels := make(map[string]string, len(configLabels)+len(appAttrs))
	for key, value := range configLabels {
		if !prometheusLabelNamePattern.MatchString(key) {
			return nil, fmt.Errorf("database metrics label name %q is invalid", key)
		}
		if key == "db_name" || key == "operation" || key == "result" {
			return nil, fmt.Errorf("database metrics label name %q is reserved", key)
		}
		labels[key] = value
	}
	for _, attr := range appAttrs {
		key := strings.ReplaceAll(string(attr.Key), ".", "_")
		if _, exists := labels[key]; exists {
			return nil, fmt.Errorf(
				"database metrics label name %q conflicts with application identity",
				key,
			)
		}
		labels[key] = attr.Value.AsString()
	}
	return labels, nil
}
