package bootstrap

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

// AppInfoBootstrap 标记 appinfo 的组装贡献已完成。
type AppInfoBootstrap struct{}

// NewAppInfoBootstrap 将已有组件接入应用组装，不启动运行时。
func NewAppInfoBootstrap(spec *app.Spec, info appinfo.AppInfo) AppInfoBootstrap {
	spec.RegisterAppInfo(info)
	log.RegisterFields(
		log.ServiceIDKey, info.ID(),
		log.ServiceNameKey, info.Name(),
		log.ServiceVersionKey, info.Version(),
	)
	return AppInfoBootstrap{}
}

// LogBootstrap 标记 log 的组装贡献已完成。
type LogBootstrap struct{}

// NewLogBootstrap 将已有组件接入应用组装，不启动运行时。
func NewLogBootstrap(spec *app.Spec, manager config.Manager, logger log.Logger) (LogBootstrap, func(), error) {
	// 启动期拒绝非法配置；后续订阅失败保留最近有效策略。
	initial := new(log.RuntimeConfig)
	if err := manager.Load("log", initial, new(log.RuntimeConfig)); err != nil {
		return LogBootstrap{}, nil, fmt.Errorf("log bootstrap: load policy: %w", err)
	}
	if err := log.ValidateRuntimeConfig(initial); err != nil {
		return LogBootstrap{}, nil, fmt.Errorf("log bootstrap: validate policy: %w", err)
	}
	global := logger
	spec.RegisterLogger(global)
	if err := log.ApplyRuntimeConfig(initial); err != nil {
		return LogBootstrap{}, nil, fmt.Errorf("log bootstrap: apply initial policy: %w", err)
	}
	cancel, err := manager.Subscribe("log", new(log.RuntimeConfig), func(_ string, value any, err error) {
		if err == nil {
			err = log.ApplyRuntimeConfig(value.(*log.RuntimeConfig))
		}
		if err != nil {
			logger.With("error", err).Error("Failed to apply log configuration")
		}
	}, new(log.RuntimeConfig))
	if err != nil {
		return LogBootstrap{}, nil, fmt.Errorf("log bootstrap: subscribe policy: %w", err)
	}

	// 保留每次安装的独立身份供 cleanup 判断所有权，不附加固定 caller 跳栈。
	installed := global.With()
	restore := log.SetLogger(installed)
	cleanup := func() {
		cancel()
		restore()
	}
	return LogBootstrap{}, cleanup, nil
}

// TracingBootstrap 标记 tracing 的组装贡献已完成。
type TracingBootstrap struct{}

// NewTracingBootstrap 将已有组件接入应用组装，不启动运行时。
func NewTracingBootstrap() (TracingBootstrap, error) {
	log.RegisterFields(
		log.TraceIDKey, tracing.TraceID(),
		log.SpanIDKey, tracing.SpanID(),
	)
	return TracingBootstrap{}, nil
}

// MetricsBootstrap 标记 metrics 的组装贡献已完成。
type MetricsBootstrap struct{}

// NewMetricsBootstrap 将已有组件接入应用组装，不启动运行时。
func NewMetricsBootstrap(spec *app.Spec, meter metrics.Metrics) MetricsBootstrap {
	spec.AddContext(func(ctx context.Context) context.Context {
		return metrics.WithMetrics(ctx, meter)
	})
	return MetricsBootstrap{}
}
