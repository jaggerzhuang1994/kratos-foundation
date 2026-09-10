# bootstrap

`pkg/bootstrap` 负责跨组件集成与应用登记。领域包只声明自身依赖，提供普通 Go 构造函数；不导入 `app`、`bootstrap` 或 Wire 来参与应用组装。组件内部创建自身 SDK、配置解析与资源管理仍留在领域包中。

## 按应用选择组件（推荐）

`internal` 保存 service、biz、data、job 和 consumer 的实现；`cmd/<app>` 是唯一的应用组件组装点。
Wire 构造当前 provider 实际依赖的实现，业务 Boot provider 接收并填充 `*bootstrap.Spec`，无需提供 `*server.Spec` 或 `*job.Spec`。

```go
// cmd/api/bootstrap.go：只选择 HTTP，未选择的 gRPC 即使配置默认开启也不会创建。
func Boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, service *service.AuthService) (bootstrap.Bootstrap, error) {
    spec.Http().Register(func(srv server.HTTPServer) error {
        auth_service_pb.RegisterAuthServiceHTTPServer(srv, service)
        return nil
    })
    return bootstrap.Bootstrap{}, nil
}
```

worker 的 provider 则可以选择任务和消费者：

```go
func Boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, task *jobimpl.Reconcile, consumer *queue.ConsumerRuntime) (bootstrap.Bootstrap, error) {
    spec.Job().RegisterCron("reconcile", "@every 1m", task)
    spec.RegisterRuntime(consumer)
    return bootstrap.Bootstrap{}, nil
}
```

以上业务类型由消费项目定义。Queue ConsumerRuntime 由 Wire 调用 queue.NewConsumerRuntime 构造，
与自定义 worker 一样通过 spec.RegisterRuntime(runtime) 登记；Spec 不再提供专用 Consumer 方法。
未调用 Http() 或 Grpc() 时不会创建服务器。
`Http()`、`Grpc()` 仅选择协议，默认仍遵循配置的 disable；显式 `.Enable()` 可以覆盖配置。
未调用 `Http()`、`Grpc()` 的 worker 不构造服务器，也不读取服务器配置；未调用 `Job()` 不构造任务管理器。
空 Spec 可用于不需要这些组件的应用；基础设施仍由 Wire 显式选择，本入口不会自动创建数据库、Redis 或消息客户端。

Wire 使用 `bootstrap.NewSpec, bootstrap.ApplicationSpec, Boot, bootstrap.NewComponentsBootstrap, bootstrap.NewApplicationBootstrap`。
bootstrap.NewSpec 创建组件声明及其持有的 app.Spec；ApplicationSpec 向基础设施 provider 暴露同一实例。
统一模式不要再提供 app.NewSpec，避免生成第二份状态或出现重复 provider。
Boot 返回 `Bootstrap`，NewComponentsBootstrap 依赖该标记，保证先声明后构造。
NewApplicationBootstrap 等待组件登记完成并返回 `StartupReady`，随后 NewKratosApp 才能冻结应用；此模式不使用旧的 NewBootstrap。
Boot 不得依赖 ComponentsBootstrap，否则形成循环；Boot 只接收 *bootstrap.Spec，直接调用 RegisterRegistrar、BeforeStart 等方法登记应用贡献。
停机策略使用 `bootstrap.NewStopPolicy` 替代 `app.NewStopPolicy`，自动从组件标记取得服务器等待时间；无服务器时为零。
业务无需提供 time.Duration 适配函数；停机策略的订阅 cleanup 继续由 Wire 在组件资源之前释放。
不要为同一组件同时使用统一入口与旧的独立 Bootstrap，否则会重复登记。

`bootstrap.Spec` 在构造期串行填充，只能组装一次；组装开始后不可再修改 Spec 或保留的 Builder。
`NewComponentsBootstrap` 不启动后台任务。成功返回的 cleanup 归 Wire，在应用停止后调用；
错误会保留原因链并释放已创建的服务器资源，失败的 app.Spec 应丢弃。
底层数据库、消息客户端和消费者资源的 cleanup 仍归各自 provider；运行时 Start/Stop 由 app 生命周期管理。
bootstrap.Spec 按模块提供 Http()、Grpc()、Job()；业务无需接收第二个 Spec。
生命周期方法直接挂在 spec 上；日志仅通过 log.WithKV、log.WithTimeFormat 等包级方法修改全局状态，不再提供 spec.Log()。
最终 app.NewApp 冻结的正是这份由私有字段持有的状态；冻结后直接调用 BeforeStart、AddMetadata 等方法会返回 app.ErrSpecFrozen。
请使用 bootstrap.NewSpec 创建统一声明，不能使用其零值；app 包仍然不依赖 server、job、queue。

```mermaid
flowchart TD
    A([cmd 选择业务实现]) --> B[Wire 构造依赖和 Spec，完成基础设施及配置观测]
    B --> P[Boot 声明组件和应用贡献]
    P --> Q{Boot 成功}
    Q -- 否 --> X
    Q -- 是 --> C{是否选中组件}
    C -- HTTP或gRPC --> D[创建 Server Runtime]
    C -- Job --> E[创建 Job Manager]
    C -- Runtime --> F[登记 Wire 已构造的 Runtime]
    C -- 无 --> G[跳过组件构造]
    D --> H{构造及登记成功}
    E --> H
    F --> H
    G --> I[ComponentsBootstrap]
    H -- 否 --> X[释放本次资源并返回错误]
    X --> Y([调用方处理错误])
    H -- 是 --> I
    I --> J[NewApplicationBootstrap 解除最终屏障]
    J --> K[NewKratosApp 冻结 app.Spec]
    K --> L([App 统一管理 Start及Stop])
```

本包暂不提供 ProviderSet；由业务组装层选择构造函数、声明 Wire 绑定并维护 injector。

| 构造函数 | 返回标记 | 组装职责 |
| --- | --- | --- |
| `NewAppInfoBootstrap(info, spec)` | `AppInfoBootstrap` | 登记身份并设置日志服务字段 |
| `NewLogBootstrap(logger, spec)` | `LogBootstrap` | 登记 Logger，安装全局 Logger 并返回恢复 cleanup |
| `NewTracingBootstrap()` | `TracingBootstrap` | 设置日志中的 trace/span 动态字段 |
| `NewMetricsBootstrap(spec, meter)` | `MetricsBootstrap` | 将默认 Meter 注入 App Context |
| `NewServerBootstrap(spec, runtime)` | `ServerBootstrap` | 登记已启用的 HTTP/gRPC Server |
| `NewJobBootstrap(spec, manager)` | `JobBootstrap` | 登记非空任务管理器并适配任务完成结果 |

这些构造函数都返回 error，登记失败时保留错误链；独立登记函数中仅 LogBootstrap 额外返回 `func()` cleanup。其他资源的 cleanup 来自领域构造函数，由 Wire 在构造失败或调用方退出时逆序执行。Bootstrap 不启动 Runtime。

`NewInfrastructureBootstrap` 汇合 AppInfo、Log、Tracing、Metrics 和 ConfigObservability 的贡献标记。业务 provider 接收 `InfrastructureBootstrap` 和所需的 `ServerBootstrap`、`JobBootstrap` 等标记，返回 `Bootstrap`。`NewBootstrap` 汇合基础设施和业务标记后，`NewKratosApp` 才调用 `app.NewApp` 冻结 Spec。

Wire 只执行最终返回值的依赖链。仅把构造函数放进 set 不保证执行；业务必须让每项贡献被最终标记引用。阶段内只保留实际依赖：仅在最终业务 provider 接收基础设施标记，不会推迟其所有参数的构造。

```mermaid
flowchart TD
    A([业务 Wire injector]) --> B[领域构造函数创建组件及 cleanup]
    B --> C{构造成功?}
    C -- 否 --> X[Wire 逆序 cleanup]
    C -- 是 --> D[NewXXXBootstrap 同步登记贡献]
    D --> E{登记成功?}
    E -- 否 --> X
    E -- 是 --> F[汇合 InfrastructureBootstrap 与 Bootstrap]
    F --> G[NewBootstrap 返回 StartupReady]
    G --> H[NewKratosApp 调用 app.NewApp 冻结 Spec]
    H --> I{App 构造成功?}
    I -- 否 --> X
    X --> Y([返回错误])
    I -- 是 --> J[返回 App 与 cleanup 给业务]
    J --> K([组装完成])
```

构造错误由调用方处理，登记函数本身不重复记录日志。NewLogBootstrap 为 Kratos 全局日志函数单独增加一层 caller 跳过，使日志指向业务调用位置；直接注入的 Logger 保持默认深度。
日志全局安装沿用单应用、逆序释放的约定；多个应用并发安装或交错释放全局 Logger 不受本包保障。优先将实例 Logger 显式注入组件。

`JobBootstrap` 的适配只转换 Manager 返回的独立 `job.ErrCompleted`，任务失败保持原样。Job 包不依赖 App 的错误契约。Queue 的 `ConsumerRuntime` 通过 Go 方法集隐式满足 `app.Runtime`；由 Wire 调用 `queue.NewConsumerRuntime` 构造，再通过 `spec.RegisterRuntime` 登记；统一入口不代为构造消费者。

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

将 `bootstrap.NewConfigObservabilityBootstrap` 加入 Wire provider 集合；
`NewInfrastructureBootstrap` 已聚合其完成标记，业务 Boot 只需依赖 `InfrastructureBootstrap`。它依赖现有
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

业务 Spec 显式开放组件声明、RegisterRegistrar、AddContext、AddMetadata、AddEndpoints、AddSignals 和四个生命周期钩子。
app.Spec 由私有字段持有，不再通过匿名嵌入暴露 RegisterAppInfo、RegisterLogger 或 Ready；
运行时使用 RegisterRuntime() 声明。ApplicationSpec 仅作为 Wire 组装桥接函数供 Foundation provider 使用，业务 Boot 不应调用它。

日志 Wire provider 仅保留 log.NewLogger，配置加载与共享状态均由 log 包内部管理。
