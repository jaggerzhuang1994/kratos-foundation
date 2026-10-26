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

组装期调用 `bootstrap.NewMetricsBootstrap(appSpec, meter, pusher, logger)`，向 `app.Spec` 追加 ContextDecorator。`app.NewApp` 会在锁外基于调用方 Context 应用它，因此业务 Runtime 可通过 Metrics Context 辅助函数取得同一个 Meter：

```go
// pusher 来自 metrics.NewPusher，未配置 metrics.push.url 时为 nil，表示不推送。
contribution, err := bootstrap.NewMetricsBootstrap(appSpec, meter, pusher, logger)
if err != nil {
	return err
}
```

该 Bootstrap 只返回完成标记，Spec 冻结后调用会 panic(app.ErrSpecFrozen)。它不启动 Runtime，也不拥有 Provider 的关闭；`NewProvider` 返回的 cleanup 仍由调用方/Wire 负责。

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
    B -- 是 --> D1[创建业务 Meter]
    D1 --> D2{通过 Bootstrap 注入?}
    D2 -- 否 --> D[直接通过 WithMetrics 注入 Context]
    D2 -- 是 --> D4{Spec 未冻结?}
    D4 -- 否 --> D3([panic ErrSpecFrozen])
    D4 -- 是 --> D5[登记 ContextDecorator 在 NewApp 冻结后注入 Context]
    D5 --> E
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

### 秒单位直方图

`NewProvider` 在构造期为所有 `Histogram` 且 `Unit == "s"` 的 OpenTelemetry instrument 配置统一 SDK View。有限桶边界（秒）为：

```text
0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05,
0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600, 1800, 3600,
7200, 21600, 43200, 86400
```

此 View 优先于 instrument 的 `WithExplicitBucketBoundaries` 建议，包括 Job、Cache、Lock 已声明的秒单位桶；所有通过该 Provider 创建的同类业务指标也受影响。统一桶同时保留 Job 原有的长任务范围，最大有限边界为 86400 秒（24 小时）；例如 3601 秒的任务进入 7200 秒桶。超过 86400 秒的样本进入 `+Inf` 桶，仍计入 `_sum` 和 `_count`，但无法据此精确估计更长任务的尾延迟。`Unit == "ms"`（例如 Redis）、未声明单位或其他单位的 instrument 保留各自的建议桶或 SDK 默认桶；通过 `PrometheusRegisterer()` 注册的原生 collector（包括数据库、Go 和进程指标）不经过该 View，桶定义保持原样。

SDK 通用默认桶的首个正边界为 5；当单位是秒而实际操作只耗时微秒时，`histogram_quantile(0.95, ...)` 会在宽桶内插值，可能显示约 4.75 秒，这不是实测单次耗时。统一细桶改善估计分辨率，但分位数仍是桶内估计；可结合 `_sum / _count` 与日志核对。

升级会改变同名直方图的 `le` 序列。滚动升级期间避免聚合不同桶布局的实例；等待全部实例升级完成，并让查询所用 `rate`/`increase` 时间窗口完全落在新版本运行期间后，再比较新旧分位数。历史窗口仍可能混合旧桶与新桶，不能将其变化直接解释为性能改善或回退。

```mermaid
flowchart TD
    A([创建或注册指标]) --> B{原生 Prometheus collector?}
    B -- 是 --> C[保留 collector 桶]
    B -- 否 --> D{Histogram 且 Unit 为 s?}
    D -- 是 --> E[应用统一秒桶 覆盖 instrument 建议桶]
    D -- 否 --> F[保留 instrument 建议或 SDK 默认桶]
    C --> G[记录样本并由实例 Registry 导出]
    E --> G
    F --> G
    G --> H{聚合窗口包含不同桶布局?}
    H -- 是 --> I[按版本隔离实例或等待窗口稳定]
    I --> J([稳定窗口核对分位数与 sum/count])
    H -- 否 --> J
```

## 一次性进程推送到 Pushgateway

一次性脚本（`job.Spec.ExitWhenDone()`，此时 `job.Manager.IsOneShot()` 为 true）常在 Prometheus 下一次抓取前退出，`job_runs_total` 等指标随进程丢失。`metrics.push`（默认关闭）在应用结束时把与 `/metrics` 相同的实例 Registry 推送到 Pushgateway 一次：

```yaml
metrics:
  push:
    url: http://pushgateway.monitoring:9091 # 省略协议按 http；不含 /metrics/job/...
    job: ""     # 分组 job 名，默认应用名
    timeout: 5s # 默认 5s，须为正数
```

`metrics.NewPusher(manager, provider, appInfo)` 在组装期读取一次配置（[需重启]）：url 为空返回 nil 表示关闭；地址或超时非法返回 error 阻止启动，错误不回显地址中的凭据。`BaseProviderSet` 已包含它；显式列出 provider 的 injector 需加入 `metrics.NewPusher` 后重新执行 `wire`。`NewMetricsBootstrap` 拿到非 nil Pusher 时登记一个停止后钩子：

- **时机**：成功、Runtime 失败、启动回滚都会执行 AfterStop，因此三种结果都会推送；此时全部 Runtime 已退出、`app_ready=0`，Provider 由 Wire cleanup 在 `Run` 返回后关闭，推送仍能采集完整数据。钩子按登记顺序执行，晚于 MetricsBootstrap 登记的业务 AfterStop 中记录的指标不会包含在本次推送中。
- **失败边界**：推送受 `timeout` 约束；连接失败、超时或 Pushgateway 非 2xx 只记录 WARN `metrics.push.failed`（字段 `push_job`、`duration`、`error`），钩子返回 nil，不改变 `Run` 结果和退出码。成功记录 INFO `metrics.push.completed`。不重试：一次性进程退出后没有后续机会，重试只会延长退出。
- **分组与标签**：分组键只有 `job`，不带 pod 或实例 ID，避免每次运行在 Pushgateway 留下一组永不过期的序列；同一 job 的并发进程会互相覆盖，需要区分时为每个脚本配置不同 `job`。PUT 替换同组上一次结果。指标自身的 `job` 标签（如 `job_runs_total` 的任务名）推送时改为 `exported_job`——Pushgateway 分组独占 `job`，`client_golang/push` 会拒绝含 `job` 标签的指标；改名与 Prometheus `honorLabels=false` 抓取时的规则一致，`/metrics` 抓取不受影响。地址中的 `user:password@` 转为 Basic Auth。
- **适用范围**：建议只对 `IsOneShot()` 的进程开启，按部署（K8s Job/CronJob 的配置）启用即可。常驻服务继续由 Prometheus 抓取，开启只会在每次停机时多一次推送，并留下可能过期的分组。Pushgateway 抓取需 `honorLabels: true`。

推送数据描述**最近一次运行**，查询时按“最近一次” gauge 理解：`push_time_seconds` 即最近运行时间（`job_last_run_timestamp_seconds` 语义），最近一次无失败时它也是最近成功时间（`job_last_success_timestamp_seconds`），单任务进程的 `job_duration_seconds_sum` 即本次耗时（`job_last_duration_seconds`）。对 Pushgateway 序列不要使用 `increase`，查询示例与 K8s Job 告警见 [一次性脚本与 CronJob 告警](../../observability/alerts.md#一次性脚本与-cronjob-告警)。框架不额外导出这三个 gauge。

```mermaid
flowchart TD
    A([组装期 NewPusher]) --> B{metrics.push.url 为空?}
    B -- 是 --> C([返回 nil 不推送])
    B -- 否 --> D{地址与 timeout 合法?}
    D -- 否 --> E([返回 error 阻止启动])
    D -- 是 --> F[NewMetricsBootstrap 登记 AfterStop 钩子]
    F --> G[App Run 结束: 成功 失败或启动回滚]
    G --> H[全部 Runtime 退出后执行 AfterStop]
    H --> I[采集实例 Registry 并把 job 标签改为 exported_job]
    I --> J[PUT /metrics/job/应用名 受 timeout 约束]
    J --> K{2xx?}
    K -- 是 --> L[INFO metrics.push.completed]
    K -- 否或超时 --> M[WARN metrics.push.failed]
    L --> N([钩子返回 nil Run 结果与退出码不变])
    M --> N
```

## 业务缓存与接入指南

`NewCacheMetrics(provider, "products")` 提供固定缓存类别的 Hit/Miss/Error/Load 记录。只有业务层知道“有效命中”和“实际回源”，它不自动操作 Redis、不改变缓存一致性或并发策略。命中率使用 hit/(hit+miss)，访问错误单独统计；Load 记录实际回源次数、结果及秒单位耗时。对象只持有 instrument 与不可变名称，没有单独 cleanup。

Provider 默认 Go collector 之外额外启用 `/cpu/classes/gc/total:cpu-seconds` 与 `/sched/latencies:seconds`，用于 GC CPU 估计开销和调度等待分布；保留现有默认 Go/Process 指标，不导出全部 runtime 指标。不同平台的 process 指标可能不同，缺失不补零。

完整可编译示例、指标设计、Grafana 查询、实例图例及可复用接入任务说明见 [业务指标接入指南](../../observability/business-metrics.md)。
