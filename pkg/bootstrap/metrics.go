package bootstrap

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

// MetricsBootstrap 标记 metrics 的组装贡献已完成。
type MetricsBootstrap struct{}

// NewMetricsBootstrap 将已有组件接入应用组装，不启动运行时。
func NewMetricsBootstrap(spec *app.Spec, meter metrics.Metrics) (MetricsBootstrap, error) {
	err := spec.AddContext(func(ctx context.Context) context.Context {
		return metrics.WithMetrics(ctx, meter)
	})
	return MetricsBootstrap{}, err
}
