package database

import (
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

// sqlMetrics 统计 GORM SQL 操作回调，不采集 SQL 文本或参数。
// 回调只在 Manager 发布前安装；运行期复用 Prometheus 自身的并发安全实现。
type sqlMetrics struct {
	operations *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	slow       *prometheus.CounterVec
	threshold  *prometheus.GaugeVec
}

func newSQLMetrics(registerer prometheus.Registerer) (*sqlMetrics, error) {
	labels := []string{"db_name", "operation", "result"}
	metrics := &sqlMetrics{
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "database_sql_operations_total",
			Help: "GORM SQL callback operations by connection, operation and result.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "database_sql_operation_duration_seconds",
			Help:    "GORM SQL callback duration in seconds, including callback processing.",
			Buckets: prometheus.DefBuckets,
		}, labels),
		slow: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "database_sql_slow_operations_total",
			Help: "GORM SQL callback operations exceeding the effective slow threshold.",
		}, labels),
		threshold: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "database_sql_slow_threshold_seconds",
			Help: "Effective GORM slow threshold in seconds; zero disables slow classification.",
		}, []string{"db_name"}),
	}
	collectors := []prometheus.Collector{metrics.operations, metrics.duration, metrics.slow, metrics.threshold}
	for i, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			// 只回滚本次成功注册的指标，保留冲突指标原有的所有权。
			for _, registered := range collectors[:i] {
				registerer.Unregister(registered)
			}
			return nil, fmt.Errorf("register database SQL metrics: %w", err)
		}
	}
	return metrics, nil
}

// install 在独立 GORM 根实例上登记固定操作标签，事务派生实例沿用相同回调。
func (m *sqlMetrics) install(db *gorm.DB, name string, slowThreshold time.Duration) error {
	callbacks := db.Callback()
	for _, operation := range []struct {
		name   string
		before func(string, func(*gorm.DB)) error
		after  func(string, func(*gorm.DB)) error
	}{
		{"create", callbacks.Create().Before("*").Register, callbacks.Create().After("*").Register},
		{"update", callbacks.Update().Before("*").Register, callbacks.Update().After("*").Register},
		{"delete", callbacks.Delete().Before("*").Register, callbacks.Delete().After("*").Register},
		{"query", callbacks.Query().Before("*").Register, callbacks.Query().After("*").Register},
		{"row", callbacks.Row().Before("*").Register, callbacks.Row().After("*").Register},
		{"raw", callbacks.Raw().Before("*").Register, callbacks.Raw().After("*").Register},
	} {
		key := "foundation:sql_metrics:" + operation.name
		if err := operation.before(key+":before", func(tx *gorm.DB) {
			if !tx.DryRun {
				tx.InstanceSet(key, time.Now())
			}
		}); err != nil {
			return fmt.Errorf("register database SQL %s start callback: %w", operation.name, err)
		}
		if err := operation.after(key+":after", func(tx *gorm.DB) {
			// 无 SQL 的前置失败和 DryRun 不代表 SQL 操作；错误仅做固定分类。
			if tx.DryRun || tx.Statement.SQL.Len() == 0 {
				return
			}
			value, ok := tx.InstanceGet(key)
			if !ok {
				return
			}
			m.record(name, operation.name, slowThreshold, time.Since(value.(time.Time)), tx.Error)
		}); err != nil {
			return fmt.Errorf("register database SQL %s finish callback: %w", operation.name, err)
		}
	}
	m.threshold.WithLabelValues(name).Set(slowThreshold.Seconds())
	return nil
}

func (m *sqlMetrics) record(name, operation string, slowThreshold, elapsed time.Duration, err error) {
	result := "success"
	if errors.Is(err, gorm.ErrRecordNotFound) {
		result = "not_found"
	} else if err != nil {
		result = "error"
	}
	m.operations.WithLabelValues(name, operation, result).Inc()
	m.duration.WithLabelValues(name, operation, result).Observe(elapsed.Seconds())
	// 与 GORM 阈值比较一致，但不受日志级别或错误优先输出分支影响。
	if slowThreshold > 0 && elapsed > slowThreshold {
		m.slow.WithLabelValues(name, operation, result).Inc()
	}
}

func (m *sqlMetrics) unregister(registerer prometheus.Registerer) {
	if m == nil {
		return
	}
	registerer.Unregister(m.operations)
	registerer.Unregister(m.duration)
	registerer.Unregister(m.slow)
	registerer.Unregister(m.threshold)
}
