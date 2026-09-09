package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/otelattr"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	clientprometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Metrics 是业务应用记录指标的默认 OpenTelemetry Meter。
type Metrics = metric.Meter

// Provider 持有实例私有的 OpenTelemetry MeterProvider 和 Prometheus Registry。
type Provider interface {
	// Meter 返回指定 instrumentation scope 的 Meter。
	Meter(name string, options ...metric.MeterOption) Metrics
	// MeterProvider 返回当前实例持有的 provider。
	MeterProvider() metric.MeterProvider
	// PrometheusGatherer 返回实例私有的 Prometheus gatherer。
	PrometheusGatherer() clientprometheus.Gatherer
	// PrometheusRegisterer 用于向实例私有 registry 注册原生 collector。
	PrometheusRegisterer() clientprometheus.Registerer
}

// NewMetrics 使用业务应用名创建默认 Meter。
func NewMetrics(provider Provider, appInfo appinfo.AppInfo) Metrics {
	return provider.Meter(appInfo.Name())
}

// provider 持有实例私有的 OpenTelemetry MeterProvider 和 Prometheus Registry。
type provider struct {
	mp   metric.MeterProvider
	prom *clientprometheus.Registry
}

// NewProvider 创建实例私有的指标 provider，并返回关闭 OpenTelemetry 管道的幂等 cleanup。
func NewProvider(
	appInfo appinfo.AppInfo,
) (Provider, func(), error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(otelattr.ServiceAttributes(appInfo)...),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create metrics resource: %w", err)
	}
	registry := clientprometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	exporter, err := otelprometheus.New(
		otelprometheus.WithRegisterer(registry),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create Prometheus exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)
	p := &provider{mp: mp, prom: registry}
	var cleanupOnce sync.Once
	return p, func() {
		cleanupOnce.Do(func() {
			// Shutdown 可以等待异步导出器收尾，但不能无限阻塞应用退出。
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := mp.Shutdown(ctx); err != nil {
				// Wire cleanup 没有错误返回通道，交给 OTel ErrorHandler 才不会静默丢失关闭失败。
				otel.Handle(fmt.Errorf("shutdown metrics provider: %w", err))
			}
		})
	}, nil
}

// Meter 直接通过 OpenTelemetry MeterProvider 获取指定 scope 的 Meter。
func (m *provider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	return m.mp.Meter(name, options...)
}

// MeterProvider 返回当前实例私有的 OpenTelemetry provider。
func (m *provider) MeterProvider() metric.MeterProvider {
	return m.mp
}

// PrometheusGatherer 返回只包含当前应用指标的采集器。
func (m *provider) PrometheusGatherer() clientprometheus.Gatherer {
	return m.prom
}

// PrometheusRegisterer 返回当前应用的原生 Prometheus 注册器。
func (m *provider) PrometheusRegisterer() clientprometheus.Registerer {
	return m.prom
}
