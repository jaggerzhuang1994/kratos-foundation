package metrics

import (
	"context"

	"go.opentelemetry.io/otel/metric"
)

type metricsCtxKey struct{}

func NewContext(ctx context.Context, metric Metrics) context.Context {
	if metric == nil {
		return ctx
	}
	return context.WithValue(ctx, metricsCtxKey{}, metric)
}

func FromContext(ctx context.Context) (m Metrics, ok bool) {
	m, ok = ctx.Value(metricsCtxKey{}).(Metrics)
	return
}

// AddCounter 累加计数器
// 用于记录单调递增的数值，如请求总数、处理总数等
// 返回是否成功记录指标
func AddCounter(ctx context.Context, name string, incr int64, options ...metric.AddOption) bool {
	m, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return m.AddCounter(ctx, name, incr, options...)
}

// RecordGauge 记录瞬时值
// 用于记录可以上下波动的数值，如当前内存使用、活跃连接数等
// 返回是否成功记录指标
func RecordGauge(ctx context.Context, name string, value int64, options ...metric.RecordOption) bool {
	m, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return m.RecordGauge(ctx, name, value, options...)
}

// RecordHistogram 记录直方图数据
// 用于记录耗时统计（如请求耗时）、数值分布等
// 返回是否成功记录指标
func RecordHistogram(ctx context.Context, name string, incr float64, options ...metric.RecordOption) bool {
	m, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return m.RecordHistogram(ctx, name, incr, options...)
}
