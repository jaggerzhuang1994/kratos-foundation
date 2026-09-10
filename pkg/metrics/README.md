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

## 记录与暴露指标

默认 Meter 使用应用名作为 instrumentation scope。以下片段中的 provider、appInfo 已按前文构造；HTTP handler 等未获得 App Context 的入口也可显式注入：

```go
ctx := metrics.WithMetrics(context.Background(), metrics.NewMetrics(provider, appInfo))
counter, err := metrics.Int64Counter(ctx, "orders_created")
if err != nil {
    return err
}
counter.Add(ctx, 1)
```

Context helper 在未注入 Meter 时返回 `metrics.ErrMetricsNotFound`，不会自动使用全局 Provider。Bootstrap 注入的是 App Context，不应假定任意外部创建的 Context 都包含它；可在组件构造期通过注入的 Meter 创建并复用 instrument。异步指标回调的注册由调用方持有，并在对应资源释放前 `Unregister`。

Provider 创建实例私有 Registry，默认包含 Go 和进程 collector；不会自动挂载 HTTP 端点。将同一个 Provider 传给 `server.NewRuntime`，或通过统一 Bootstrap 组装，才能由 `server.http.metrics` 暴露；业务 HTTP 默认启用 `/metrics`，独立管理地址、禁用规则见 [Server 监控端点监听地址](../server/README.md#监控端点监听地址)。原生 collector 也须注册到该实例的 `PrometheusRegisterer()`，注册到全局 Registry 的指标不会自动出现在这里。

```mermaid
flowchart TD
    A([构造 Provider]) --> B{Resource 与 Registry 构造成功?}
    B -- 否 --> C([返回错误 由调用方处理])
    B -- 是 --> D[创建业务 Meter 并注入 Context]
    D --> E{Context 含 Meter?}
    E -- 否 --> F([helper 返回 ErrMetricsNotFound])
    E -- 是 --> G[创建 instrument]
    G -- 失败 --> C
    G -- 成功 --> R[记录业务指标]
    R --> H[同一 Provider 注入 Server Runtime]
    H --> I{HTTP metrics 监听启用?}
    I -- 否 --> J[仅持有指标 不暴露 HTTP]
    I -- 是 --> K[抓取实例 Registry 输出指标]
    J --> L[停止 Runtime 后执行 Wire cleanup]
    K --> L
    L --> M[最多 5s 关闭 SDK Provider]
    M -- 失败 --> N[OTel ErrorHandler shutdown metrics provider]
    M -- 成功 --> O([结束])
    N --> O
```
