# bootstrap

`pkg/bootstrap` 负责跨组件集成与应用登记。领域包只声明自身依赖，提供普通 Go 构造函数；不导入 `app`、`bootstrap` 或 Wire 来参与应用组装。组件内部创建自身 SDK、配置解析与资源管理仍留在领域包中。

本包暂不提供 ProviderSet；由业务组装层选择构造函数、声明 Wire 绑定并维护 injector。

| 构造函数 | 返回标记 | 组装职责 |
| --- | --- | --- |
| `NewAppInfoBootstrap(info, spec, shared)` | `AppInfoBootstrap` | 登记身份并设置日志服务字段 |
| `NewLogBootstrap(shared, spec)` | `LogBootstrap` | 登记 Logger，安装全局 Logger 并返回恢复 cleanup |
| `NewTracingBootstrap(shared)` | `TracingBootstrap` | 设置日志中的 trace/span 动态字段 |
| `NewMetricsBootstrap(spec, meter)` | `MetricsBootstrap` | 将默认 Meter 注入 App Context |
| `NewServerBootstrap(spec, runtime)` | `ServerBootstrap` | 登记已启用的 HTTP/gRPC Server |
| `NewJobBootstrap(spec, manager)` | `JobBootstrap` | 登记非空任务管理器并适配任务完成结果 |

这些构造函数都返回 error，登记失败时保留错误链；仅 LogBootstrap 额外返回 `func()` cleanup。其他资源的 cleanup 来自领域构造函数，由 Wire 在构造失败或调用方退出时逆序执行。Bootstrap 不启动 Runtime。

`NewInfrastructureBootstrap` 汇合 AppInfo、Log、Tracing、Metrics 的贡献标记。业务 provider 接收 `InfrastructureBootstrap` 和所需的 `ServerBootstrap`、`JobBootstrap` 等标记，返回 `UserBootstrap`。`NewBootstrap` 汇合基础设施和业务标记后，`NewKratosApp` 才调用 `app.NewApp` 冻结 Spec。

Wire 只执行最终返回值的依赖链。仅把构造函数放进 set 不保证执行；业务必须让每项贡献被最终标记引用。阶段内只保留实际依赖：仅在最终业务 provider 接收基础设施标记，不会推迟其所有参数的构造。

```mermaid
flowchart TD
    A([业务 Wire injector]) --> B[领域构造函数创建组件及 cleanup]
    B --> C{构造成功?}
    C -- 否 --> X[Wire 逆序 cleanup]
    C -- 是 --> D[NewXXXBootstrap 同步登记贡献]
    D --> E{登记成功?}
    E -- 否 --> X
    E -- 是 --> F[汇合 InfrastructureBootstrap 与 UserBootstrap]
    F --> G[NewBootstrap 返回最终标记]
    G --> H[NewKratosApp 调用 app.NewApp 冻结 Spec]
    H --> I{App 构造成功?}
    I -- 否 --> X
    X --> Y([返回错误])
    I -- 是 --> J[返回 App 与 cleanup 给业务]
    J --> K([组装完成])
```

构造错误由调用方处理，登记函数本身不重复记录日志。日志全局安装沿用单应用、逆序释放的约定；多个应用并发安装或交错释放全局 Logger 不受本包保障。优先将实例 Logger 显式注入组件。

`JobBootstrap` 的适配只转换 Manager 返回的独立 `job.ErrCompleted`，任务失败保持原样。Job 包不依赖 App 的错误契约。Queue 的 `ConsumerRuntime` 通过 Go 方法集隐式满足 `app.Runtime`，业务按消费者实例显式登记。

```mermaid
flowchart TD
    A([App 启动 Job Runtime]) --> B[适配器调用 Manager.Start]
    B --> C{返回结果}
    C -- job.ErrCompleted --> D[转换为 app.ErrStopRequested]
    D --> E[App 正常停止]
    C -- 任务失败 --> F[原样返回错误供 App 收敛]
    C -- nil --> G[正常返回]
    E --> H([结束])
    F --> H
    G --> H
```

真实 Wire 生成、逆序 cleanup 与失败回滚由 `wire_integration_test.go` 在临时模块验证，不提交或手改生成副本。
该 fixture 还启动 HTTP 订单服务并访问临时 SQLite，检查事务提交、回滚、具名连接隔离和旧配置拒绝；
通过根目录 `make test-business` 运行，验收范围及发布检查见 [v2 迁移清单](../../MIGRATION_V2.md)。

## 配置观测组装

将 `bootstrap.NewConfigObservabilityBootstrap` 加入 Wire provider 集合，并在业务 Bootstrap 中
显式依赖 `bootstrap.ConfigObservabilityBootstrap`，使注册发生在启动前。它依赖现有
`config.Manager` 和 `metrics.Provider`，返回标记、幂等 cleanup、error；Wire 负责在关闭
metrics Provider 和配置 Manager 之前取消注册。自定义 Manager 未实现 `config.StatusReader`
会明确返回错误，重复注册也会失败，不会静默替换已有 collector。

该可选组装直接接入实例私有 registry，现有 HTTP metrics 端点自动暴露指标，无新增轮询任务：

| 指标 | 含义 |
| --- | --- |
| foundation_config_watcher_up | 监听正常为 1，终止或关闭为 0 |
| foundation_config_revision | 本地接受快照序号 |
| foundation_config_updates_total{result="accepted\|rejected"} | 包括初始加载的接受次数、后续拒绝次数 |
| foundation_config_last_success_timestamp_seconds | 最近接受快照时间 |
| foundation_config_subscription_overloads_total | 累计订阅过载次数 |
| foundation_config_subscriptions_accepting | 仍接受更新的订阅数 |
| foundation_config_subscription_queue_depth | 当前注册订阅待处理通知总数，含终止通知 |
| foundation_config_callbacks_running | 当前注册订阅中正在运行的回调数 |
| foundation_config_callback_duration_seconds_sum / _count | 完成回调耗时总和、次数，无分位数 |

告警起点：`foundation_config_watcher_up == 0`，以及
`increase(foundation_config_subscription_overloads_total[5m]) > 0`。回调平均耗时可使用
`rate(foundation_config_callback_duration_seconds_sum[5m]) / rate(foundation_config_callback_duration_seconds_count[5m])`，无回调时分母为零，不解释为故障。
不因长期无配置更新告警，也不把快照接受或回调结束解释为业务应用成功。指标没有 key、订阅 ID、
配置值或原始错误标签，避免高基数及敏感信息。详细定位使用 Status 和已有错误日志。

```mermaid
flowchart TD
    A([Wire 组装]) --> B{Manager 实现 StatusReader?}
    B -- 否 --> C[返回组装错误]
    B -- 是 --> D[注册实例私有 collector]
    D --> E{注册成功?}
    E -- 否 --> C
    E -- 是 --> F[启动 HTTP metrics 端点]
    F --> G[并发抓取时读取状态副本 不执行回调]
    G --> H[输出固定指标]
    H --> I[停机后 Wire cleanup 一次性取消注册]
    I --> J([关闭依赖并结束])
    C --> J
```

Wire 业务 fixture 同时验证配置指标实际可抓取，以及默认 healthz、readyz 在应用启动后的响应。

ServerBootstrap 同时登记业务监听与 `Runtime.ManagementServers()` 返回的独立监控监听；管理端口不加入业务服务发现。地址复用规则见 [server 监控监听配置](../server/README.md#监控端点监听地址)。
