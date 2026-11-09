package database

import (
	"errors"
	"fmt"
	"regexp"
	"sync"

	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
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
// 指标只带 database.metrics.labels 常量标签，不附加 service_name、service_instance_id 等资源属性：
// 实例 ID 每个进程不同，作为标签会让每次重启都换一整套序列；服务身份由采集侧的 app/pod 标签表达，与其他组件一致。
func newMetricsCollector(
	connectionFactory *connectionFactory,
	config *config_pb.Database,
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
	labels, err := validateMetricLabels(conf.GetLabels())
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

// validateMetricLabels 复制用户配置的常量标签，并拒绝非法名称和固定维度标签。
func validateMetricLabels(configLabels map[string]string) (map[string]string, error) {
	labels := make(map[string]string, len(configLabels))
	for key, value := range configLabels {
		if !prometheusLabelNamePattern.MatchString(key) {
			return nil, fmt.Errorf("database metrics label name %q is invalid", key)
		}
		if key == "db_name" || key == "operation" || key == "result" {
			return nil, fmt.Errorf("database metrics label name %q is reserved", key)
		}
		labels[key] = value
	}
	return labels, nil
}
