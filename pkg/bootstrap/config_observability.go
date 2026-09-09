package bootstrap

import (
	"fmt"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// ConfigObservabilityBootstrap 标记配置运行指标已经注册到实例私有 registry。
type ConfigObservabilityBootstrap struct{}

// NewConfigObservabilityBootstrap 注册配置观测；Wire 应聚合标记，并在 provider 关闭前调用 cleanup。
// Manager 必须实现 config.StatusReader；不启动轮询 goroutine，也不读取配置值。
func NewConfigObservabilityBootstrap(manager config.Manager, provider metrics.Provider) (ConfigObservabilityBootstrap, func(), error) {
	reader, ok := manager.(config.StatusReader)
	if !ok {
		return ConfigObservabilityBootstrap{}, nil, fmt.Errorf("config manager does not implement StatusReader")
	}
	collector := &configCollector{reader: reader}
	registry := provider.PrometheusRegisterer()
	if err := registry.Register(collector); err != nil {
		return ConfigObservabilityBootstrap{}, nil, fmt.Errorf("register config metrics: %w", err)
	}
	var once sync.Once
	return ConfigObservabilityBootstrap{}, func() { once.Do(func() { registry.Unregister(collector) }) }, nil
}

type configCollector struct{ reader config.StatusReader }

// Describe 通过相同的采样路径声明固定描述符，不依赖当前是否存在订阅。
func (c *configCollector) Describe(ch chan<- *prometheus.Desc) { prometheus.DescribeByCollect(c, ch) }

// Collect 只读取状态副本；不使用配置 key、订阅 ID 或原始错误作为指标标签。
func (c *configCollector) Collect(ch chan<- prometheus.Metric) {
	status := c.reader.Status()
	emit := func(name, help string, kind prometheus.ValueType, value float64) {
		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc("foundation_config_"+name, help, nil, nil), kind, value)
	}
	up := 0.0
	if status.WatcherRunning {
		up = 1
	}
	emit("watcher_up", "Whether the configuration watcher is running.", prometheus.GaugeValue, up)
	emit("revision", "Local accepted snapshot revision, including initial load.", prometheus.GaugeValue, float64(status.Revision))
	updated := 0.0
	if !status.LastSuccess.IsZero() {
		updated = float64(status.LastSuccess.UnixNano()) / 1e9
	}
	emit("last_success_timestamp_seconds", "Time of the last accepted snapshot; no updates does not imply failure.", prometheus.GaugeValue, updated)
	desc := prometheus.NewDesc("foundation_config_updates_total", "Accepted or rejected snapshots, including initial load.", []string{"result"}, nil)
	ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(status.AcceptedUpdates), "accepted")
	ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(status.RejectedUpdates), "rejected")
	emit("subscription_overloads_total", "Subscriptions terminated by overload since manager construction.", prometheus.CounterValue, float64(status.Overloads))
	accepting, pending, running := 0, 0, 0
	for _, sub := range status.Subscriptions {
		if sub.Accepting {
			accepting++
		}
		pending += sub.Pending
		if sub.Running {
			running++
		}
	}
	emit("subscriptions_accepting", "Subscriptions still accepting notifications.", prometheus.GaugeValue, float64(accepting))
	emit("subscription_queue_depth", "Total pending notifications, including terminal notifications.", prometheus.GaugeValue, float64(pending))
	emit("callbacks_running", "Callbacks running in registered subscriptions.", prometheus.GaugeValue, float64(running))
	// 累计耗时与次数可计算平均值；不伪造未采集的分位数。
	ch <- prometheus.MustNewConstSummary(prometheus.NewDesc("foundation_config_callback_duration_seconds", "Completed callback durations, including initial replay; not business success acknowledgements.", nil, nil), status.Callbacks, status.CallbackDuration.Seconds(), nil)
}
