package metrics

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CacheMetrics 记录业务缓存语义，不执行缓存读写或自动判定 redis.Nil。
// 指标和底层 Provider 可并发使用，不保存请求状态。
type CacheMetrics struct {
	// name 固定缓存类别名，避免高基数标签。
	name string
	// lookups 按命中、未命中和错误累计查找次数。
	lookups metric.Int64Counter
	// loads 实际回源次数。
	loads metric.Int64Counter
	// duration 实际回源耗时。
	duration metric.Float64Histogram
}

// NewCacheMetrics 在构造期创建业务缓存指标，Provider 仍由组装层释放。
func NewCacheMetrics(provider Provider, name string) (*CacheMetrics, error) {
	if name == "" || strings.TrimSpace(name) != name {
		return nil, errors.New("cache metrics name must be non-empty without surrounding whitespace")
	}
	meter := provider.Meter("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics/cache")
	lookups, err := meter.Int64Counter("business_cache_lookups_total")
	if err != nil {
		return nil, err
	}
	loads, err := meter.Int64Counter("business_cache_loads_total")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("business_cache_load_duration_seconds", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10))
	if err != nil {
		return nil, err
	}
	return &CacheMetrics{name: name, lookups: lookups, loads: loads, duration: duration}, nil
}

// Hit 记录业务确认的有效缓存命中，不包括过期或解码失败。
func (c *CacheMetrics) Hit(ctx context.Context) { c.lookup(ctx, "hit") }

// Miss 记录缓存不存在或业务确认不可用，需要回源的结果。
func (c *CacheMetrics) Miss(ctx context.Context) { c.lookup(ctx, "miss") }

// Error 记录缓存访问失败；单独统计，不把网络故障混进命中率分母。
func (c *CacheMetrics) Error(ctx context.Context) { c.lookup(ctx, "error") }

func (c *CacheMetrics) lookup(ctx context.Context, result string) {
	c.lookups.Add(ctx, 1, metric.WithAttributes(attribute.String("cache_name", c.name), attribute.String("result", result)))
}

// Load 记录一次实际回源的结果和耗时；即使并发请求合并，也只由执行回源的一方记录。
func (c *CacheMetrics) Load(ctx context.Context, elapsed time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(attribute.String("cache_name", c.name), attribute.String("result", result))
	c.loads.Add(ctx, 1, attrs)
	c.duration.Record(ctx, elapsed.Seconds(), attrs)
}
