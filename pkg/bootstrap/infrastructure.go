package bootstrap

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

// AppInfoBootstrap 标记 appinfo 的组装贡献已完成。
type AppInfoBootstrap struct{}

// NewAppInfoBootstrap 将已有组件接入应用组装，不启动运行时。
func NewAppInfoBootstrap(info appinfo.AppInfo, spec *app.Spec, shared *log.SharedState) (AppInfoBootstrap, error) {
	if err := spec.RegisterAppInfo(info); err != nil {
		return AppInfoBootstrap{}, fmt.Errorf("appinfo bootstrap: register app info: %w", err)
	}
	if err := shared.WithKV(
		log.ServiceIDKey, info.ID(),
		log.ServiceNameKey, info.Name(),
		log.ServiceVersionKey, info.Version(),
	); err != nil {
		return AppInfoBootstrap{}, fmt.Errorf("appinfo bootstrap: apply service log fields: %w", err)
	}
	return AppInfoBootstrap{}, nil
}

// LogBootstrap 标记 log 的组装贡献已完成。
type LogBootstrap struct{}

// NewLogBootstrap 将已有组件接入应用组装，不启动运行时。
func NewLogBootstrap(shared *log.SharedState, spec *app.Spec) (LogBootstrap, func(), error) {
	global := log.NewLogger(shared)
	if err := spec.RegisterLogger(global); err != nil {
		return LogBootstrap{}, nil, fmt.Errorf("log bootstrap: register app logger: %w", err)
	}
	previous := log.GetLogger()
	log.SetLogger(global)
	cleanup := func() {
		if log.GetLogger() == global {
			log.SetLogger(previous)
		}
	}
	return LogBootstrap{}, cleanup, nil
}

// TracingBootstrap 标记 tracing 的组装贡献已完成。
type TracingBootstrap struct{}

// NewTracingBootstrap 将已有组件接入应用组装，不启动运行时。
func NewTracingBootstrap(shared *log.SharedState) (TracingBootstrap, error) {
	if err := shared.WithKV(
		log.TraceIDKey, tracing.TraceID(),
		log.SpanIDKey, tracing.SpanID(),
	); err != nil {
		return TracingBootstrap{}, fmt.Errorf("tracing bootstrap: apply trace log fields: %w", err)
	}
	return TracingBootstrap{}, nil
}

// MetricsBootstrap 标记 metrics 的组装贡献已完成。
type MetricsBootstrap struct{}

// NewMetricsBootstrap 将已有组件接入应用组装，不启动运行时。
func NewMetricsBootstrap(spec *app.Spec, meter metrics.Metrics) (MetricsBootstrap, error) {
	err := spec.AddContext(func(ctx context.Context) context.Context {
		return metrics.WithMetrics(ctx, meter)
	})
	return MetricsBootstrap{}, err
}
