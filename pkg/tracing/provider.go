package tracing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/otelattr"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// provider 提供应用追踪能力。
type provider struct {
	// tp 应用私有的底层 Provider，用于创建业务 Tracer。
	tp trace.TracerProvider
}

var (
	_ Provider = (*provider)(nil)
	_ Provider = (*disabledProvider)(nil)
)

// NewProvider 统一创建 exporter、sampler 和 provider，避免业务或 Wire 感知遥测组装细节。
// cleanup 会在退出前限时刷新并关闭 provider。
func NewProvider(
	configManager foundationconfig.Manager,
	appInfo appinfo.AppInfo,
) (Provider, func(), error) {
	nopCleanup := func() {}
	config, err := loadConfig(configManager, appInfo)
	if err != nil {
		return nil, nil, err
	}
	serviceAttributes := otelattr.ServiceAttributes(appInfo)
	if config.GetDisable() {
		return newDisabledProvider(), nopCleanup, nil
	}
	hotConfig, cancelSampler, err := foundationconfig.NewHotReloadValue[config_pb.Tracing](configManager, "tracing", config)
	if err != nil {
		return nil, nil, err
	}
	sampler, err := newDynamicSampler(hotConfig)
	if err != nil {
		cancelSampler()
		return nil, nil, err
	}
	tracingResource, err := newTracingResource(serviceAttributes)
	if err != nil {
		cancelSampler()
		return nil, nil, err
	}
	exporter, err := newExporter(config)
	if err != nil {
		cancelSampler()
		return nil, nil, err
	}
	tp, cleanup := newTracerProvider(exporter, sampler, tracingResource)
	var once sync.Once
	return newProvider(tp), func() {
		once.Do(func() {
			cancelSampler()
			cleanup()
		})
	}, nil
}

// newTracingResource 合并 OTel 默认资源和应用身份，使 traces 与 metrics 使用一致的资源维度。
func newTracingResource(serviceAttributes []attribute.KeyValue) (*resource.Resource, error) {
	result, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(serviceAttributes...),
	)
	if err != nil {
		return nil, fmt.Errorf("create tracing resource: %w", err)
	}
	return result, nil
}

// newTracerProvider 组装 SDK provider，并返回带退出超时的幂等 cleanup。
func newTracerProvider(
	exporter tracesdk.SpanExporter,
	sampler tracesdk.Sampler,
	tracingResource *resource.Resource,
) (trace.TracerProvider, func()) {
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSampler(sampler),
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(tracingResource),
	)

	var cleanupOnce sync.Once
	return tp, func() {
		cleanupOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				// Wire cleanup 没有错误返回通道，交给 OTel ErrorHandler 才不会静默丢失尾批 span。
				otel.Handle(fmt.Errorf("shutdown tracing provider: %w", err))
			}
		})
	}
}

// newProvider 持有应用私有的 OpenTelemetry TracerProvider。
func newProvider(tp trace.TracerProvider) *provider {
	return &provider{tp: tp}
}

// Disabled 表示真实追踪 provider 已启用。
func (t *provider) Disabled() bool {
	return false
}

// TracerProvider 返回当前实例私有的 OpenTelemetry provider。
func (t *provider) TracerProvider() trace.TracerProvider {
	return t.tp
}

// Tracer 返回指定 instrumentation scope 的追踪器。
func (t *provider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return t.tp.Tracer(name, options...)
}

// disabledProvider 提供显式禁用且永不采样的 TracerProvider。
type disabledProvider struct {
	// provider 复用基础 Provider 接口实现。
	*provider
}

// newDisabledProvider 保留有效 SpanContext 供日志关联，但不记录或导出 Span。
func newDisabledProvider() *disabledProvider {
	tp := tracesdk.NewTracerProvider(tracesdk.WithSampler(tracesdk.NeverSample()))
	return &disabledProvider{provider: newProvider(tp)}
}

// Disabled 表示该追踪 provider 明确处于禁用状态。
func (t *disabledProvider) Disabled() bool {
	return true
}

// newExporter 将已校验配置转换为 OTLP HTTP 导出器选项。
func newExporter(config *config_pb.Tracing) (tracesdk.SpanExporter, error) {
	exporterConfig := config.GetExporter()
	var opts []otlptracehttp.Option

	if exporterConfig.GetEndpointUrl() != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(exporterConfig.GetEndpointUrl()))
	}

	opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.Compression(exporterConfig.GetCompression())))

	if exporterConfig.GetHeaders() != nil {
		opts = append(opts, otlptracehttp.WithHeaders(exporterConfig.GetHeaders()))
	}

	if exporterConfig.GetTimeout().AsDuration() > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(exporterConfig.GetTimeout().AsDuration()))
	}

	if exporterConfig.GetRetry() != nil {
		opts = append(opts, otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         exporterConfig.GetRetry().GetEnabled(),
			InitialInterval: exporterConfig.GetRetry().GetInitialInterval().AsDuration(),
			MaxInterval:     exporterConfig.GetRetry().GetMaxInterval().AsDuration(),
			MaxElapsedTime:  exporterConfig.GetRetry().GetMaxElapsedTime().AsDuration(),
		}))
	}

	return otlptracehttp.New(context.Background(), opts...)
}
