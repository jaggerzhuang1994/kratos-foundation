// Package observability 定义组件共用的观测依赖组合：日志、链路与指标 Provider。
// queue.Observability 与 kafka.Observability 都是它的别名，同一个值可直接传给 Queue、Kafka 与 Outbox。
package observability

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// Observability 组合应用注入的观测依赖；只借用 Provider，不创建资源，cleanup 仍由原 provider 负责。
type Observability struct {
	// Logger 输出组件运行日志。
	Logger log.Logger
	// Tracing 提供链路追踪。
	Tracing tracing.Provider
	// Metrics 提供指标记录。
	Metrics metrics.Provider
}

// New 组合观测依赖，可直接作为 Wire provider。
func New(logger log.Logger, tracingProvider tracing.Provider, metricsProvider metrics.Provider) Observability {
	return Observability{Logger: logger, Tracing: tracingProvider, Metrics: metricsProvider}
}
