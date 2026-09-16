package queue

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Observability 是任务投递与执行所需的日志、追踪和指标依赖。
// 借用应用已有实例，不拥有资源；三个依赖均为必需项。
type Observability struct {
	Logger  log.Logger
	Tracing tracing.Provider
	Metrics metrics.Provider
}

// NewObservability 复用应用注入的观测依赖，供显式组装及 Wire 自动构造。
// 不创建资源、不读取全局实例、不改变启用状态；cleanup 仍由原 provider 负责。
func NewObservability(logger log.Logger, tracingProvider tracing.Provider, metricsProvider metrics.Provider) Observability {
	return Observability{Logger: logger, Tracing: tracingProvider, Metrics: metricsProvider}
}

// RegisterStats 为一个固定业务队列名注册同步于指标采集的只读回调，无后台 goroutine。
// timeout 必须为正；source 必须遵守 Context 取消。同一 Provider/队列名只能注册一次。
// 返回的 cleanup 由组装层在关闭 source 连接和 Provider 前调用；不释放借用资源。
func RegisterStats(name string, source StatsProvider, provider metrics.Provider, timeout time.Duration) (func() error, error) {
	if name == "" || strings.TrimSpace(name) != name || timeout <= 0 {
		return nil, errors.New("queue stats requires a queue name and positive timeout")
	}
	meter := provider.Meter("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue")
	tasks, err := meter.Int64ObservableGauge("queue_tasks")
	if err != nil {
		return nil, err
	}
	age, err := meter.Float64ObservableGauge("queue_oldest_ready_age_seconds", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	success, err := meter.Int64ObservableGauge("queue_stats_collection_success")
	if err != nil {
		return nil, err
	}
	known, err := meter.Int64ObservableGauge("queue_stats_oldest_ready_known")
	if err != nil {
		return nil, err
	}
	registration, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		now := time.Now()
		stats, err := source.Stats(ctx, now)
		attrs := metric.WithAttributes(attribute.String("queue_destination", name))
		// 失败不输出任务数或年龄，不将不可用源误报为空队列；错误交给 OTel 错误处理边界。
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			observer.ObserveInt64(success, 0, attrs)
			observer.ObserveInt64(known, 0, attrs)
			return err
		}
		observer.ObserveInt64(success, 1, attrs)
		for _, value := range []struct {
			state string
			count int64
		}{
			{"ready", stats.Ready}, {"scheduled", stats.Scheduled}, {"running", stats.Running}, {"failed", stats.Failed},
		} {
			observer.ObserveInt64(tasks, value.count, metric.WithAttributes(attribute.String("queue_destination", name), attribute.String("state", value.state)))
		}
		if !stats.OldestReadyKnown {
			observer.ObserveInt64(known, 0, attrs)
			return nil
		}
		observer.ObserveInt64(known, 1, attrs)
		seconds := float64(0)
		if stats.Ready > 0 {
			seconds = max(0, now.Sub(stats.OldestReadyAt).Seconds())
		}
		observer.ObserveFloat64(age, seconds, attrs)
		return nil
	}, tasks, age, success, known)
	if err != nil {
		return nil, err
	}
	return registration.Unregister, nil
}
