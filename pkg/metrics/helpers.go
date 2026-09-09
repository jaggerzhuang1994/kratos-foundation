package metrics

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/metric"
)

// ErrMetricsNotFound 表示 Context 中没有注入业务 Metrics。
var ErrMetricsNotFound = errors.New("default metrics not found in context")

type defaultMetricsKey struct{}

// WithMetrics 把默认业务 Metrics 注入 Context。
func WithMetrics(ctx context.Context, value Metrics) context.Context {
	return context.WithValue(ctx, defaultMetricsKey{}, value)
}

func metricsFromContext(ctx context.Context) (Metrics, bool) {
	value, ok := ctx.Value(defaultMetricsKey{}).(Metrics)
	return value, ok
}

// Int64Counter 使用 Context 中的 Metrics 创建只增不减的整数累计量。
// 适用于请求数、任务完成数、消息消费数等由事件驱动的总量；不应记录负增量。
func Int64Counter(
	ctx context.Context,
	name string,
	options ...metric.Int64CounterOption,
) (metric.Int64Counter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64Counter(name, options...)
}

// Int64UpDownCounter 使用 Context 中的 Metrics 创建可增可减的整数累计量。
// 适用于并发请求数、活跃连接数等通过开始 +1、结束 -1 维护的状态。
func Int64UpDownCounter(
	ctx context.Context,
	name string,
	options ...metric.Int64UpDownCounterOption,
) (metric.Int64UpDownCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64UpDownCounter(name, options...)
}

// Int64Histogram 使用 Context 中的 Metrics 创建整数分布指标。
// 适用于批次大小、消息字节数、重试次数等需要观察分布和桶计数的整数值。
func Int64Histogram(
	ctx context.Context,
	name string,
	options ...metric.Int64HistogramOption,
) (metric.Int64Histogram, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64Histogram(name, options...)
}

// Int64Gauge 使用 Context 中的 Metrics 创建整数当前值指标。
// 适用于直接报告队列长度、线程数、分片数等绝对值；若状态由增减事件维护，应优先使用 Int64UpDownCounter。
func Int64Gauge(
	ctx context.Context,
	name string,
	options ...metric.Int64GaugeOption,
) (metric.Int64Gauge, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64Gauge(name, options...)
}

// Int64ObservableCounter 使用 Context 中的 Metrics 创建异步整数只增累计量。
// 适用于在采集时从外部状态读取进程 CPU tick、系统累计操作数等单调增长的整数总量。
func Int64ObservableCounter(
	ctx context.Context,
	name string,
	options ...metric.Int64ObservableCounterOption,
) (metric.Int64ObservableCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64ObservableCounter(name, options...)
}

// Int64ObservableUpDownCounter 使用 Context 中的 Metrics 创建异步整数可增可减量。
// 适用于在采集时查询活跃会话数、已分配资源数等可上下变化的整数状态。
func Int64ObservableUpDownCounter(
	ctx context.Context,
	name string,
	options ...metric.Int64ObservableUpDownCounterOption,
) (metric.Int64ObservableUpDownCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64ObservableUpDownCounter(name, options...)
}

// Int64ObservableGauge 使用 Context 中的 Metrics 创建异步整数当前值。
// 适用于在采集时读取队列长度、线程池大小、文件描述符数等当前绝对值。
func Int64ObservableGauge(
	ctx context.Context,
	name string,
	options ...metric.Int64ObservableGaugeOption,
) (metric.Int64ObservableGauge, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Int64ObservableGauge(name, options...)
}

// Float64Counter 使用 Context 中的 Metrics 创建只增不减的浮点累计量。
// 适用于 CPU 使用秒数、累计能耗等需要小数精度的总量；不应记录负增量。
func Float64Counter(
	ctx context.Context,
	name string,
	options ...metric.Float64CounterOption,
) (metric.Float64Counter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64Counter(name, options...)
}

// Float64UpDownCounter 使用 Context 中的 Metrics 创建可增可减的浮点累计量。
// 适用于资源预留量、进行中任务权重等通过浮点增减量维护的连续状态。
func Float64UpDownCounter(
	ctx context.Context,
	name string,
	options ...metric.Float64UpDownCounterOption,
) (metric.Float64UpDownCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64UpDownCounter(name, options...)
}

// Float64Histogram 使用 Context 中的 Metrics 创建浮点分布指标。
// 适用于请求耗时、处理时长、响应大小等需要观察桶计数和分位数的连续值。
func Float64Histogram(
	ctx context.Context,
	name string,
	options ...metric.Float64HistogramOption,
) (metric.Float64Histogram, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64Histogram(name, options...)
}

// Float64Gauge 使用 Context 中的 Metrics 创建浮点当前值指标。
// 适用于 CPU 使用率、内存利用率、温度等直接采样的当前连续值。
func Float64Gauge(
	ctx context.Context,
	name string,
	options ...metric.Float64GaugeOption,
) (metric.Float64Gauge, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64Gauge(name, options...)
}

// Float64ObservableCounter 使用 Context 中的 Metrics 创建异步浮点只增累计量。
// 适用于在采集时读取累计 CPU 秒数、累计能耗等单调增长的连续总量。
func Float64ObservableCounter(
	ctx context.Context,
	name string,
	options ...metric.Float64ObservableCounterOption,
) (metric.Float64ObservableCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64ObservableCounter(name, options...)
}

// Float64ObservableUpDownCounter 使用 Context 中的 Metrics 创建异步浮点可增可减量。
// 适用于在采集时查询当前资源预留量、进行中工作权重等可上下变化的连续状态。
func Float64ObservableUpDownCounter(
	ctx context.Context,
	name string,
	options ...metric.Float64ObservableUpDownCounterOption,
) (metric.Float64ObservableUpDownCounter, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64ObservableUpDownCounter(name, options...)
}

// Float64ObservableGauge 使用 Context 中的 Metrics 创建异步浮点当前值。
// 适用于在采集时读取 CPU 使用率、内存利用率、温度等当前连续值。
func Float64ObservableGauge(
	ctx context.Context,
	name string,
	options ...metric.Float64ObservableGaugeOption,
) (metric.Float64ObservableGauge, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.Float64ObservableGauge(name, options...)
}

// RegisterCallback 使用 Context 中的 Metrics 注册异步指标回调。
// 回调会在每次采集周期被调用，适合从外部系统或当前运行状态批量观测 Observable 指标。
func RegisterCallback(
	ctx context.Context,
	callback metric.Callback,
	instruments ...metric.Observable,
) (metric.Registration, error) {
	meter, ok := metricsFromContext(ctx)
	if !ok {
		return nil, ErrMetricsNotFound
	}
	return meter.RegisterCallback(callback, instruments...)
}
