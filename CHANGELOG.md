# Changelog

本文件记录面向调用方的版本变化。版本是否已经发布以 Git tag 和对应提交为准；“计划中”内容不能作为已发布能力依赖。

## 计划中

本节尚未发布。包含不兼容变更，升级前逐项确认“升级风险”。

### 升级风险

- **应用类型由 `*kratos.App` 改为 `*app.App`**：App 自行实现 Run/Stop 生命周期，不再包装 `kratos.App`。`bootstrap.NewKratosApp` 更名为 `bootstrap.NewApp`；业务 injector 的返回类型改为 `*app.App` 后重新执行 `wire`，`main` 中的 `app.Run()` 不变。`*app.App` 实现 `kratos.AppInfo`，`kratos.FromContext` 仍可读取身份。
- **生产环境不再返回错误堆栈**：HTTP 响应 metadata 与 gRPC `ErrorInfo` 中的 `err_stack` 仅在 `env.ExposeDiagnostics()` 为 true 时携带（local/dev/test 始终、pre 需 `APP_DEBUG=true`、prod 一律不带）。依赖跨服务远端堆栈排障的 prod 场景需改用服务端日志。见 [诊断出口环境约束](pkg/errors/README.md#诊断出口环境约束)。
- **日志事件、文案与字段统一**：全框架日志按 [日志格式约定](AGENTS.md#日志格式约定) 收敛，msg 改为小写“主语+结果”短语（如 `servers assembled`、`client config rejected; keeping previous`），由 `TestFrameworkLogConventions` 持续校验。依赖下列旧名的告警、看板或检索需同步调整：

  | 旧 | 新 |
  | --- | --- |
  | `kafka.consume.retry` / `kafka.consume.failed` / `kafka.consume.dead_lettered` | `kafka.message.retry_scheduled` / `kafka.message.failed` / `kafka.message.dead_lettered` |
  | `queue.retry.scheduled` | `queue.task.retry_scheduled` |
  | `queue.consumer.disabled` | `queue.worker.disabled` |
  | `server.middleware.update.rejected` | `server.middleware.rejected` |
  | `registry.consul.discovery.retry` / `registry.consul.heartbeat.retry` | `registry.consul.discovery.retrying` / `registry.consul.heartbeat.retrying` |
  | Kafka 日志字段 `kafka.destination` / `kafka.consumer` / `kafka.dead_letter_destination` / `kafka.error` | `destination` / `consumer` / `dead_letter_destination` / `reason` |
  | Queue 日志字段 `cause` | `error` |
  | 访问日志 `kind` / `latency`（字符串） | `transport` / `duration`（Duration） |
  | 移除恒定或冗余字段 | `server.middleware.*` 的 `config_key`、`server.readiness.changed` 的 `transport`、错误边界日志的 `endpoint`、`server.listener.cleanup.failed` 的 `transport`/`management_listeners` |

  `consul.client.probing` 由 INFO 降为 DEBUG；Kafka 指标标签不变。
- **配置热更新日志事件更名**：`config.watch` / `config.change`（每个订阅一条 INFO）改为每轮一条 INFO `config.snapshot.changed`（字段 `changed`，点分路径）及逐订阅 DEBUG `config.subscription.replayed` / `config.subscription.notified`；`config.loaded` 去掉冗余的 `source_count`。依赖旧事件名或 `changed_paths` 的告警与看板需同步调整。

### Added

- `app_ready` 指标（`bootstrap.NewMetricsBootstrap` 注册，签名改为返回 error）与 `/readyz`、gRPC 健康检查共用就绪状态，覆盖没有 HTTP 探针的 Worker、gRPC 服务、常驻进程；新增告警 `FoundationAppNotReady`、`FoundationAppUnavailable`。
- 监控模板新增 `foundation-records` recording rules（按 env/cluster/namespace/app 预聚合请求、错误、延迟桶、就绪、消费失败、Job 失败与最老就绪任务）、舰队总览面板 `foundation-fleet-overview`，服务总览首行新增 Foundation 应用就绪率；面板检查加载同一份规则文件做合成样本校验。
- gRPC 默认注册与应用就绪联动的 `grpc.health.v1.Health`（`custom_health` 或业务自行注册时让出），替代 Kratos 启动即报 SERVING 的内置实现。
- App 恢复 Runtime `Start/Stop/AbortStartup` 中的 panic，记录 `app.runtime.panicked` 并统一停机（执行停止钩子与服务注销），不再直接崩溃。
- `env.ExposeDiagnostics()` 统一判断是否允许向调用方返回内部诊断，非法环境值按拒绝处理且不 panic。
- `server.middleware.updated` 增加 `changed` 字段，列出实际替换的中间件。

### Changed

- 系统信号与父 Context 取消从 `Run` 开始即转为统一停止，启动阶段收到 SIGTERM 也会执行停止钩子与启动前回滚。Runtime 的真实故障全部按发生顺序汇总返回（根因在前）。
- `app.StopPolicy` 在订阅通知时即时校验并记录 `app.stop_policy.updated`（新增 `previous`）/ `app.stop_policy.rejected`，解码失败也记为后者；不再输出 `version`。
- `config.NewHotReloadValue` 改为先加载初值再登记订阅，依赖 Manager 下一轮回放覆盖期间的更新。
- `database.WithSQLTxOptions(nil)` 表示驱动默认选项，嵌套事务可接受；`Transaction` 不再为 nil 回调或 nil 选项返回错误（编程错误直接 panic）。
- 日志热路径：caller 按调用地址缓存帧解析结果，字段过滤、module 归一化、格式化、去空与去重合并为一遍处理，输出内容不变；启用级别下单条日志开销约降低 6 倍。
- Client：已构建版本的 `AcquireClient` / release 走无锁分片计数快速路径，16 并发下约 6 倍吞吐，租约与退役语义不变。
- HTTP 监听、Queue Worker、Kafka ConsumerRuntime 与错误树判断改为共享的精简实现，外部行为不变。

## v2.4.1

### Added

- Registry 新增内置 `driver: "null"`，返回 `Resource{Disabled: true}`，不提供注册或发现能力；`app.registry` 可选择该具名实例关闭本应用注册。无需额外空导入，其他实例及配置源仍按原有规则工作。配置与流程见 [Registry 配置关闭注册](pkg/registry/README.md#通过配置关闭服务注册)。

### Changed

- `server.tracing.disable`、`database.tracing.disable`、`redis.tracing.disable` 和 `client.clients.*.tracing.disable` 省略或局部追踪段为空消息时，继承组件启动时全局 `tracing.disable` 的有效值；局部显式 `true` / `false` 优先。全局字段省略时，local 环境默认 `true`，其他受支持环境默认 `false`。
- Server 与 Client 的局部追踪开关支持热更新，有效配置快照移除局部覆盖时恢复启动默认值；全局开关以及 Database、Redis 的局部开关仍需重启。注入的 `tracing.Provider.Disabled()` 仍是硬门槛，局部显式 `false` 不能恢复已禁用 Provider 的记录、采样或导出能力。具体边界与流程见 [Tracing 默认值](pkg/tracing/README.md#局部追踪默认值)。
- 观测配置仅调整 tracing 默认值继承，metrics 的配置默认值、开关和端点行为保持不变。

### Migration

- **local 环境的追踪缺省行为改变**：全局与局部开关均省略时，Database 不再安装 tracing 插件，Redis 不再安装 tracing hook。需要保留追踪记录与导出时，显式设置全局 `tracing.disable: false`，并确认组件未显式禁用。组件边界见 [Database](pkg/database/README.md#tracing-默认值) 与 [Redis](pkg/redis/README.md)。
- 默认配置源合并会保留省略字段，仅删除源文件中的局部覆盖不保证从有效快照中移除该值；热更新回退规则见 [Server](pkg/server/README.md) 与 [Client](pkg/client/README.md)。
- 使用禁用注册实例时，YAML 的驱动名必须写为 `"null"`；需要服务发现的客户端应选择提供 Discovery 的其他实例。

## v2.4.0

本版本包含不兼容变更，升级前逐项确认下面的“升级风险”。

### 升级风险

- **HTTP 监听地址可能静默改变**：HTTP 监听改由 Foundation 持有，`HTTP().Option(http.Address/Network/Listener(...))` 会被最终设置覆盖。只通过原生 option 指定地址、未配置 `server.http.addr` 的应用，升级后监听默认的 `0.0.0.0:8000`，不会报错。升级前把地址迁到 `server.http.addr` / `server.http.network` 配置，自定义监听改用 `HTTP().Listener(listener)`。
- **Redis 队列可能启动失败**：`contrib/queue/redis.NewStore` 在构造期要求连接启用 `context_timeout_enabled`，未启用时返回错误并中止启动（此前仅 `Stats` 调用失败）。升级前在对应 Redis 连接配置中设置 `context_timeout_enabled: true`。
- **编译期不兼容**：`bootstrap.NewServerBootstrap`、`bootstrap.NewJobBootstrap` 末尾增加业务 `Bootstrap` 标记参数，Wire 组装重新生成即可，手工调用需传入业务 Boot 返回的标记；`server.HTTPBuilder` 接口新增 `Listener` 方法，自行实现该接口的代码需要补齐。
- **日志契约变化**：事件名统一加领域前缀，按旧事件名编写的日志查询和告警需按下方迁移表替换；`server.endpoint.registered` 与 `job.execution.started/finished` 由 INFO 降为 DEBUG。

### Added

- Runtime 可选实现 `AbortStartup(context.Context) error`；Endpoint 或 BeforeStart 失败时，应用按登记逆序回收尚未启动的资源（如已准备的 HTTP 监听）。
- `server.HTTPBuilder.Listener` 声明由 Foundation 接管并释放的自定义监听。
- AES 字段支持 `*AESDecryptString` / `*AESDecryptBytes`，nil 指针写为 SQL `NULL`。

### Changed

- 日志事件名统一为 `<领域>.<对象>.<动作>`，可按 `event=queue.*`、`event=server.*` 前缀归类；App 生命周期事件补充固定 msg，`app.assembled` 增加 `pid`。
- `server.assembled` 以 `http`、`grpc`、`management` 配置地址和 `stop_delay` 替代 `http_enabled`、`grpc_enabled`、`management_listeners`；`config.loaded` 的 `sources` 由 Go 类型名改为来源包名（如 `[env file]`）。
- Queue 投递失败不再记录 `enqueue.failed`，错误同步返回给投递方，由 span 状态与投递指标表达；`queue.failure.callback_failed` 增加 `reason=error|panic|timeout`。
- 访问摘要删除 `component` 字段，事件名直接区分 `server.request.completed` 与 `client.request.completed`。
- `NewServerBootstrap` 与 `NewJobBootstrap` 分别依赖业务 Boot 完成标记，二者不再相互依赖；应用在 Ready 前请求停止时，等待 Ready 的 Job 立即退出。

### Migration

API 与配置迁移细节见 [V2 迁移指南](MIGRATION_V2.md)。按事件名检索或告警的日志查询需要替换为新名称：

| 旧事件 | 新事件 |
| --- | --- |
| `request.completed`（component=server/client） | `server.request.completed` / `client.request.completed` |
| `request.failed`、`request.panic.recovered` | `server.request.failed`、`server.request.panic.recovered` |
| `readiness.changed`、`endpoint.registered` | `server.readiness.changed`、`server.endpoint.registered` |
| `websocket.*` | `server.websocket.*` |
| `worker.started`、`worker.stopped`、`consumer.disabled` | `queue.worker.started`、`queue.worker.stopped`、`queue.consumer.disabled` |
| `task.execution.started`、`task.execution.finished`、`task.failed` | `queue.task.execution.started`、`queue.task.execution.finished`、`queue.task.failed` |
| `retry.scheduled`、`lease.lost`、`storage.failed`、`failure.callback_failed` | `queue.retry.scheduled`、`queue.lease.lost`、`queue.storage.failed`、`queue.failure.callback_failed` |
| `enqueue.failed` | 已删除，改为处理 `Dispatch`/`Post` 返回的错误 |
| `gorm.query` | `database.gorm.query` |
| `consul.heartbeat.*`、`consul.discovery.*`、`consul.registration.restored`、`consul.cleanup.failed` | 分别加 `registry.` 前缀 |

## v2.1.1 – v2.3.3

未在本文件记录，变更以对应 Git tag 的提交历史为准。

## v2.1.0

### Added

- Queue 新增可选 `Operations` 契约，提供元数据分页、详情、条件删除/取消/重试及有界保留期清理；不内置统一管理 API 或 UI。
- Job 新增触发跳过、当前等待数和等待耗时指标。
- Log 新增 `AtLevel` / `DebugOnly` 条件字段；`DebugOnly` 同时响应请求级 debug，可让同一条 Info 访问日志按请求展开诊断字段。
- Server 在启动前逐条记录最终业务 HTTP、gRPC 和 metrics/health 注册端点；Job 和 Queue 增加统一的执行开始/结束事件与耗时字段。
- 建立变更记录，并明确平台终止 TLS/mTLS、Foundation 内部应用链路使用明文的信任边界。

### Changed

- JSON Schema 外部合并保留根级约束；同名 definition 仅在 JSON 语义相同时接受，冲突时停止生成。
- MySQL 服务端状态指标改由独立 `mysqld-exporter` 等基础设施采集器负责；Database Manager 只保留应用连接池和 SQL 操作指标。
- Bootstrap 登记的 Job Runtime 改为等待应用 Ready 后启动，并在启动任务前记录名称、最终调度规则和注册调用点。
- GORM Queue 以 `Task.ID` 作为唯一公开主键，MySQL/SQLite 分别用 VARBINARY/BLOB 保留逐字节身份，内部 `generation` 仅用于防止 ABA；任务正文不再重复保存 ID/AvailableAt，三个任务时间字段改为 SQL 时间列并校验 MySQL TIMESTAMP 范围。
- 请求日志按摘要与诊断字段分层：deadline 细节、完整错误链和 panic 堆栈仅在 Debug 事件或请求级 debug 中展开，常规故障日志仍保留稳定分类与紧凑错误摘要。

### Removed

- 删除应用进程内基于 `SHOW STATUS` 的 MySQL 状态轮询及其 `database.metrics.mysql` 配置。

### Migration

- 删除部署配置中的 `database.metrics.mysql`；如需 MySQL 全局状态与 InnoDB 指标，在平台监控层部署并授权 `mysqld-exporter`。
- Queue 运维入口必须由业务自行实现鉴权、审计、脱敏和限流；`List` 不返回正文，正文仅由 `Get` 获取。
- 应用业务端口与管理端口均不可直接暴露到不可信网络；TLS/mTLS 在平台层完成。
- v2.1 的 GORM Queue 表不兼容旧主键、`identity` 和整数时间列；升级时停止旧 Worker，清空并按当前模型重建任务表，不提供旧任务原地迁移。
