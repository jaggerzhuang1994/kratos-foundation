# Metrics

`pkg/metrics` 提供业务与 Wire 使用的公共 `Provider`、默认 `Metrics` 和 Context 指标辅助函数。应用只通过 `NewProvider` 构造实例，并负责调用返回的幂等 cleanup。

```go
provider, cleanup, err := metrics.NewProvider(appInfo)
if err != nil {
	return err
}
defer cleanup()

meter := metrics.NewMetrics(provider, appInfo)
```

`metrics.go` 定义公共契约和默认 Meter 构造，并使用非导出的 `provider` 管理 OpenTelemetry SDK Resource、MeterProvider、Prometheus Registry 与关闭状态；`helpers.go` 提供 Context 指标辅助函数。需要原生 Prometheus collector 时，通过公共 Provider 的 `PrometheusRegisterer` 注册；采集端使用 `PrometheusGatherer`。

组装期调用 `bootstrap.NewMetricsBootstrap(appSpec, meter)`，向 `app.Spec` 追加 ContextDecorator。`app.NewApp` 会在锁外基于调用方 Context 应用它，因此业务 Runtime 可通过 Metrics Context 辅助函数取得同一个 Meter：

```go
contribution, err := bootstrap.NewMetricsBootstrap(appSpec, meter)
```

该 Bootstrap 不启动 Runtime，也不拥有 Provider 的关闭；`NewProvider` 返回的 cleanup 仍由调用方/Wire 负责。
