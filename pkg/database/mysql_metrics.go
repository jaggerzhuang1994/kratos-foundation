package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	defaultMetricsRefreshInterval = 15 * time.Second
	defaultMySQLMetricsPrefix     = "gorm_status_"
	metricsQueryTimeout           = 5 * time.Second
)

var prometheusMetricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// mysqlMetricsCollector 周期采集并暴露 MySQL 状态变量。
type mysqlMetricsCollector struct {
	// log 刷新与指标登记失败的日志器。
	log log.Logger
	// db 借用的 SQL 连接池，由连接工厂负责关闭。
	db *sql.DB
	// interval 有效刷新间隔，依次取 MySQL 专用配置、公共刷新间隔和 15s 默认值。
	interval time.Duration
	// prefix MySQL 指标名称前缀，空配置解析为 gorm_status_。
	prefix string
	// variables 变量白名单；空集合允许所有合法数值变量。
	variables map[string]struct{}
	// registerer 登记和注销当前刷新器指标的注册器。
	registerer prometheus.Registerer
	// metrics 已成功登记的指标，构造期及单个刷新循环维护，循环退出后注销。
	metrics map[string]*mysqlStatusMetric

	// values 最近一次完整成功的数值快照，刷新失败保留旧值；受 mu 保护。
	values map[string]float64
	// cancel 停止后台刷新；尚未启动时为 nil。
	cancel context.CancelFunc
	// done 刷新循环退出时关闭，供清理等待。
	done chan struct{}
	// mu 保护快照读取和替换。
	mu sync.RWMutex
}

// newMySQLMetricsCollector 校验动态指标名并构造尚未启动的 MySQL 采集器。
func newMySQLMetricsCollector(
	db *sql.DB,
	defaultInterval time.Duration,
	config *config_pb.GormMetrics_Mysql,
	logger log.Logger,
	registerer prometheus.Registerer,
) (*mysqlMetricsCollector, error) {
	if db == nil {
		return nil, errors.New("database MySQL metrics connection pool is nil")
	}
	if logger == nil {
		return nil, errors.New("database MySQL metrics logger is nil")
	}
	interval := config.GetInterval().AsDuration()
	if interval <= 0 {
		interval = defaultInterval
	}
	if interval <= 0 {
		interval = defaultMetricsRefreshInterval
	}
	prefix := strings.TrimSpace(config.GetPrefix())
	if prefix == "" {
		prefix = defaultMySQLMetricsPrefix
	}
	if !prometheusMetricNamePattern.MatchString(prefix + "value") {
		return nil, fmt.Errorf("database metrics MySQL prefix %q is invalid", prefix)
	}
	variables := make(map[string]struct{}, len(config.GetVariableNames()))
	for _, variable := range config.GetVariableNames() {
		variable = strings.TrimSpace(variable)
		if variable == "" {
			return nil, errors.New("database metrics MySQL variable name is empty")
		}
		if !prometheusMetricNamePattern.MatchString(prefix + variable) {
			return nil, fmt.Errorf(
				"database metrics MySQL variable name %q is invalid",
				variable,
			)
		}
		variables[variable] = struct{}{}
	}
	collector := &mysqlMetricsCollector{
		log:        logger,
		db:         db,
		interval:   interval,
		prefix:     prefix,
		variables:  variables,
		registerer: registerer,
		metrics:    make(map[string]*mysqlStatusMetric),
		values:     make(map[string]float64),
		done:       make(chan struct{}),
	}
	// 白名单的描述符在启动前就可完整登记；动态变量在首次成功查询后登记。
	for variable := range variables {
		if err := collector.registerVariable(variable); err != nil {
			collector.close()
			return nil, err
		}
	}
	return collector, nil
}

// mysqlStatusMetric 暴露单个 MySQL 状态变量；快照中已消失的变量不再输出值。
type mysqlStatusMetric struct {
	// collector 提供最近一次数值快照的采集器。
	collector *mysqlMetricsCollector
	// variable 对应的 MySQL 状态变量名。
	variable string
	// desc 该变量的固定指标描述符。
	desc *prometheus.Desc
}

func (metric *mysqlStatusMetric) Describe(ch chan<- *prometheus.Desc) { ch <- metric.desc }

func (metric *mysqlStatusMetric) Collect(ch chan<- prometheus.Metric) {
	metric.collector.mu.RLock()
	value, exists := metric.collector.values[metric.variable]
	metric.collector.mu.RUnlock()
	// 慢 scrape 的 channel 发送不持有快照锁。
	if exists {
		ch <- prometheus.MustNewConstMetric(metric.desc, prometheus.GaugeValue, value)
	}
}

// registerVariable 只由构造或单个刷新 goroutine 调用；cleanup 等待退出后再注销。
// 每个真实指标都有固定 Describe，空白名单也不会形成无法注销的 unchecked collector。
func (c *mysqlMetricsCollector) registerVariable(variable string) error {
	if _, exists := c.metrics[variable]; exists {
		return nil
	}
	metric := &mysqlStatusMetric{
		collector: c,
		variable:  variable,
		desc:      prometheus.NewDesc(c.prefix+variable, "MySQL SHOW STATUS variable "+variable+".", nil, nil),
	}
	if err := c.registerer.Register(metric); err != nil {
		return fmt.Errorf("register database MySQL metric %q: %w", variable, err)
	}
	// 只有成功注册的实例属于本刷新器；注册冲突不能取得他人指标的注销所有权。
	c.metrics[variable] = metric
	return nil
}

// start 在后台立即取得首个快照，避免禁用 GORM Ping 时构造器被隐式阻塞。
func (c *mysqlMetricsCollector) start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)
}

// run 立即刷新一次后按配置周期刷新，取消后通知 close。
func (c *mysqlMetricsCollector) run(ctx context.Context) {
	defer close(c.done)
	c.refresh(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh(ctx)
		}
	}
}

// refresh 只在整次查询成功时替换快照，避免短暂错误清空上一份可用指标。
func (c *mysqlMetricsCollector) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, metricsQueryTimeout)
	defer cancel()
	rows, err := c.db.QueryContext(ctx, "SHOW STATUS")
	if err != nil {
		if parent.Err() == nil {
			c.log.With("error", err).Warn("Failed to query MySQL metrics")
		}
		return
	}
	defer rows.Close()

	values := make(map[string]float64)
	for rows.Next() {
		var variable string
		var rawValue string
		if err := rows.Scan(&variable, &rawValue); err != nil {
			c.log.With("error", err).Warn("Failed to decode a MySQL metrics row")
			return
		}
		if len(c.variables) > 0 {
			if _, ok := c.variables[variable]; !ok {
				continue
			}
		}
		if !prometheusMetricNamePattern.MatchString(c.prefix + variable) {
			continue
		}
		value, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			continue
		}
		values[variable] = value
	}
	if err := rows.Err(); err != nil {
		c.log.With("error", err).Warn("Failed while reading MySQL metrics rows")
		return
	}
	// 先完成描述符注册，再公布快照，确保 pedantic Registry 只采集已登记的指标。
	for variable := range values {
		if err := c.registerVariable(variable); err != nil {
			c.log.With("error", err).Warn("Failed to register a MySQL metric")
			return
		}
	}
	c.mu.Lock()
	c.values = values
	c.mu.Unlock()
}

// close 停止 MySQL 刷新循环；nil 接收者代表未启用该采集器。
func (c *mysqlMetricsCollector) close() {
	if c == nil {
		return
	}
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	for variable, metric := range c.metrics {
		c.registerer.Unregister(metric)
		delete(c.metrics, variable)
	}
}

var _ prometheus.Collector = (*mysqlStatusMetric)(nil)
