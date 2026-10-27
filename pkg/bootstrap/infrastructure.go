package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
		"env", info.Metadata()["env"],
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
			logger.WithModule("log").With("event", "log.policy.rejected", "error", err).Warn("log policy rejected; keeping previous")
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

// NewMetricsBootstrap 把 Meter 注入应用 Context，并注册 app_ready 就绪指标，不启动运行时。
// app_ready 与 /readyz、gRPC 健康检查读取同一就绪状态：1 表示已就绪，0 表示启动中、停机中或启动失败；
// 没有 HTTP 探针的队列消费者、自定义常驻进程与一次性任务同样可据此告警。
// pusher 非 nil 时登记停止后钩子，在全部 Runtime 退出后向 Pushgateway 推送一次；
// 推送失败只记录 WARN，钩子返回 nil，不改变 Run 的结果和进程退出码。
func NewMetricsBootstrap(spec *app.Spec, meter metrics.Metrics, pusher *metrics.Pusher, logger log.Logger) (MetricsBootstrap, error) {
	spec.AddContext(func(ctx context.Context) context.Context {
		return metrics.WithMetrics(ctx, meter)
	})
	_, err := meter.Int64ObservableGauge("app_ready",
		metric.WithDescription("Whether the application is ready to serve: 1 ready, 0 starting, stopping or failed."),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			ready := int64(0)
			if spec.Ready() {
				ready = 1
			}
			observer.Observe(ready)
			return nil
		}))
	if err != nil {
		return MetricsBootstrap{}, fmt.Errorf("register app_ready metric: %w", err)
	}
	// panic 被恢复、配置/策略被拒绝等事件原先只有日志；按事件名计数后可直接告警（FoundationPanicRecovered、
	// FoundationConfigRejected）。事件名是框架代码中的固定常量，标签基数可控。
	events, err := meter.Int64Counter("log_events_total",
		metric.WithDescription("Warn-or-higher log events that indicate a recovered panic or a rejected configuration, by event name."))
	if err != nil {
		return MetricsBootstrap{}, fmt.Errorf("register log_events_total metric: %w", err)
	}
	log.SetEventCounter(func(event string) {
		events.Add(context.Background(), 1, metric.WithAttributes(attribute.String("event", event)))
	})
	if pusher != nil {
		spec.AfterStop(func(ctx context.Context) error {
			pushMetrics(ctx, pusher, logger.WithModule("metrics"))
			return nil
		})
	}
	return MetricsBootstrap{}, nil
}

// pushMetrics 执行一次推送并记录结果；一次性进程退出后无法再被抓取，推送是指标的最后出口。
func pushMetrics(ctx context.Context, pusher *metrics.Pusher, logger log.Logger) {
	started := time.Now()
	if err := pusher.Push(ctx); err != nil {
		logger.WithContext(ctx).With("event", "metrics.push.failed", "push_job", pusher.Job(), "duration", time.Since(started), "error", err).Warn("metrics push failed")
		return
	}
	logger.WithContext(ctx).With("event", "metrics.push.completed", "push_job", pusher.Job(), "duration", time.Since(started)).Info("metrics pushed to pushgateway")
}
