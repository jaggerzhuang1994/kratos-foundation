# Changelog

本文件记录面向调用方的版本变化。版本是否已经发布以 Git tag 和对应提交为准；“计划中”内容不能作为已发布能力依赖。

## 计划中

### 升级风险

- **Consul 注册的 gRPC 端点改用 gRPC 健康检查**：原 TCP 检查改为由 Consul 调用实例的 `grpc.health.v1.Health/Check`，结果与 `/readyz` 一致。应用未就绪、停机中或 readiness 依赖检查失败时，实例在 Consul 中为 critical，服务发现不再返回它；readiness 检查中只应保留“缺少它整个服务都不可用”的依赖。启用心跳时该检查不参与自动注销，依赖恢复后实例立即重新可见。需要旧行为时设置 `registry.instances.<name>.options.registry.disable_grpc_health_check: true`。业务自行注册 gRPC 健康服务（`server.grpc.custom_health`）时，Consul 结果取决于业务实现。
- **Consul 健康检查与心跳默认间隔由 10s 改为 5s**：TTL 随之由 20s 改为 10s，Consul agent 收到的心跳与检查请求加倍；端点检查默认连续失败 2 次才判定 critical（`failures_before_critical`，原为 1 次）。显式配置 `healthcheck_internal` 的应用不受影响。

### Added

- **registry/consul**：`options.registry.disable_grpc_health_check` 与 `options.registry.failures_before_critical`。

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**其他**

- ⚠️ 不兼容：**plugins**：add /v2 module suffix for versioned plugin releases（`ded9774`）
- **client-v2**：depend on kratos-foundation v2.7.0（`d70d8b0`）
<!-- changelog:auto:end -->

## v2.7.0（2026-10-27）

### 升级风险

- **服务改为按服务名注册**：注册中心的服务名由 Server 实际承载的 proto 服务推导（proto 所在目录名），不再使用 `APP_NAME` / 可执行文件名；每个服务名只挂载承载它的端点，未在 proto 声明的手写路由等入口归入应用名并告警。Queue、Cron 等不承载业务服务的进程不再注册。客户端按同一规则解析，按目录名约定（`api/<service_name>/*.proto` 与二进制同名）的项目名字不变。
- **删除 `app.endpoints` 与 `app.Spec.AddEndpoints` / `bootstrap.Spec.AddEndpoints`**：注册端点只来自 Server 的 Endpointer，对外地址与监听地址不同时改用 `server.http.endpoint` / `server.grpc.endpoint`。`app.metadata`、`app.registry` 的字段编号随之调整，配置按字段名解析不受影响；残留的 `app.endpoints` 被忽略。
- **删除服务选项 `kratos_foundation_client.client_name` 及 `kratos_foundation_client/client.proto`**：服务名只由 proto 所在目录推导（顶层文件取文件名），客户端插件与服务注册共用同一规则；import 该文件的 proto 需删除 import 与选项后重新生成，连接名不同时用 `NewXxxWithConnName` 指定。
- **proto 位于 `v1`、`v2beta1` 这类版本目录时客户端生成与服务注册失败**：版本目录名无法区分服务，需改为 `api/<service_name>/*.proto` 布局。
- **管理端口运行时实现 `Endpointer`**：`server.Runtime.ManagementServers()` 的运行时改为公布端点并声明 `app.RegistrationExempt`，App 不注册它们；自行组装、直接按 `Endpointer` 收集注册地址的代码需同时判断 `RegistrationExempt`。
- **生成器子模块改为 `/v2` 模块路径并按版本发布**：`protoc-gen-jsonschema`、`protoc-gen-kratos-foundation-errors-v2`、`protoc-gen-kratos-foundation-client-v2` 的模块路径追加 `/v2`，以子模块 tag `cmd/<plugin>/v2.7.0` 发布（提交 `ded9774`，晚于根模块 tag，提交记录列在后续版本），安装改为 `go install github.com/jaggerzhuang1994/kratos-foundation/cmd/<plugin>/v2@v2.7.0`，二进制名不变。按提交号安装不可用；引用旧路径 `cmd/protoc-gen-jsonschema/...` 的项目保持原伪版本，不会被自动升级。

### Added

- `pkg/servicename`：proto service 到服务名的唯一推导规则（目录名 → 文件名 → Go 包名，版本名报错），客户端插件、服务注册与网关共用；`IsFramework` 识别健康检查、反射、channelz 与 `kratos.api.Metadata`。
- 按服务名注册多个实例：一个进程承载多个服务名时分别注册，实例元数据带 `app_name` / `process_id`；任一实例注册失败时整体回滚。见 [注册名与端点](pkg/app/README.md#注册名与端点)。
- `server.HandleMethod(fullMethod, handler)` 与 `HTTPBuilder.WebSocketMethod` / `WebSocketMethodWithConfig`：按 proto 方法全名挂载手写 HTTP 处理器与 WebSocket 端点，method、path 取自 `google.api.http`，operation 为方法全名，路由随 proto 声明保持一致并归入对应服务名。WebSocket 方法须为只声明一条 GET 规则的流式 rpc；其 service 同时注册到 gRPC 时输出 WARN `server.websocket.grpc_conflict`。见 [按 proto 声明挂载](pkg/server/README.md#按-proto-声明挂载)。
- `app.ServiceNamer` / `app.RegistrationExempt`：Runtime 汇报承载的服务名、声明不参与服务注册的可选接口；`server.Runtime` 的业务 HTTP/gRPC Server 按路由与已注册服务收集服务名，见 [服务名收集](pkg/server/README.md#服务名收集)。

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- **server**：mount handwritten HTTP and WebSocket endpoints by proto method（`f0b2197`）
- ⚠️ 不兼容：**app**：register services by name instead of application name（`10bc684`）
- **server**：report service names served by HTTP and gRPC servers（`b9fbc47`）
- ⚠️ 不兼容：**servicename**：derive service names only from the proto directory（`0f6f795`）

**文档**

- **changelog**：prepare v2.7.0 release notes（`b8d5987`）
- explain service-name registration in the demo and minimal example（`7668705`）
<!-- changelog:auto:end -->

## v2.6.0（2026-10-27）

### 升级风险

- **`/metrics` 与健康检查默认改到独立管理端口 `0.0.0.0:8001`**：`server.http.metrics.addr` / `health.addr` 省略时不再挂在业务 HTTP 上（此前默认复用业务端口，`/metrics` 会随业务端口对外暴露），与业务部署模板的 management 端口一致；业务 HTTP 关闭的 Worker 也默认有健康检查与指标端点。仍需挂在业务端口的应用把两项显式设为 `""`；K8s 探针与 ServiceMonitor 指向 8001。
- **业务 HTTP 默认限制请求体 32 MiB**：超过返回 413 `REQUEST_BODY_TOO_LARGE`。文件上传等大请求在 `server.http.request_body.routes` 中按路径放大或设为 0（不限制）。
- **SQL 日志默认隐去参数值（prod / 未开调试的 pre）**：`database.gorm.logger.parameterized_queries` 未配置时按环境取默认值，与错误堆栈的诊断出口约束一致；local/dev/test 仍记录完整 SQL。需要生产完整参数的排查显式设为 `false`。
- **事务内不能再切换数据库连接**：同一 Manager 的事务 Context 中用 `database.UseConnection` 选择另一个已存在连接（含在其上嵌套 `Transaction`）返回 `database.ErrConnectionInTransaction`；此前会静默沿用事务所在连接，把数据写进错误的库。选择事务所在连接仍复用事务。依赖旧行为的代码需改为在事务外访问其他连接。
- **gRPC 服务端默认放宽 keepalive 检测**：允许客户端最短 10s 一次 ping，且允许无活动 RPC 时 ping（gRPC 原生为 5min 且需活动流），可用 `server.grpc.keepalive_min_time` / `keepalive_permit_without_stream` 调整。业务自行传入 Kratos `grpc.Options` 会整体替换该设置。
- **Queue Worker 遇到存储故障不再退出**：领取、确认、释放、失败持久化时的数据库或 Redis 故障（断连、`storage_timeout` 超时、主从切换、损坏记录）改为在循环内按 500ms 起倍增、上限 30s 退避重试，记录 `WARN queue.storage.retrying`，恢复后记录 `INFO queue.storage.recovered`；此前会记录 `ERROR queue.storage.failed` 并让整个应用停机重启。`queue_consumer_runtime_failures_total` 仍按每次故障累加，FoundationQueueRuntimeFailure 告警口径不变；依赖“存储故障即重启 Pod”的部署需改为按该告警处理。Store 返回不满足契约的领取结果仍会退出。

### Added

- 业务 HTTP Server 默认 `server.http.read_header_timeout` 10s 与 `idle_timeout` 120s（Kratos 原生不设连接级超时，存在慢速请求头占用连接的风险），可配置。
- 业务 HTTP 请求体上限 `server.http.request_body`：默认 32 MiB，`routes` 按精确 path 或最长 prefix 覆盖（文件上传接口放大，0 表示不限制）；声明长度超限直接 413，分块传输在读取时截断，Bind 与业务读取超限都返回 413 `REQUEST_BODY_TOO_LARGE`。WebSocket 升级请求与 gRPC 不受影响。
- `pkg/observability.Observability`：`queue.Observability` 与 `kafka.Observability` 改为它的类型别名，同一个值可直接传给 Queue、Kafka 与 Outbox，已有字面量与 `queue.NewObservability` 写法不变。
- `kafka.NewProducerWithObservability(factory, config, obs)`：一步创建带指标、链路与日志的 Producer，等价于 `NewProducer` + `NewManagedProducer`。
- `server.HandleRoute(method, path, operation, handler)`：注册经过 Foundation 服务端中间件链（恢复、截止时间、限流、日志、指标、链路、错误归一化）的手写 HTTP 路由，处理器通过 `*server.HTTPRequest` 取路径参数、查询参数与绑定请求体，返回值按服务器编码器输出。原生 `Route(...).GET/POST`、`HandleFunc`、`Handle`、`HandlePrefix` 保留，文档标注其不经过中间件。
- 开启重试的客户端额外上报逻辑调用指标 `client_calls_code_total` / `client_calls_seconds`（重试结束后记一次最终结果与总耗时），新增预聚合 `app_downstream:client_calls:rate5m`、`app_downstream:client_call_errors:rate5m`、`app_downstream:client_calls_seconds_bucket:rate5m`（未上报时退回尝试级）；平台总览与网关详情的按下游表格并列“尝试 5xx”与“调用 5xx（重试后）”。
- `totp.Authenticator.Verify` / `VerifyAt`：验证成功时返回匹配的时间步，供认证服务持久化最后一次成功的时间步以防重放；`Authenticate` / `AuthenticateAt` 不变。
- `database.AutoMigrate(ctx, manager, models...)`：固定在主库迁移，配置 `read_replicas` 时不会因 Migrator 的存在性检查落到副本而重复建表。
- `bootstrap.Spec.HTTP()` / `GRPC()`，与 `server.Spec` 命名一致；原 `Http()` / `Grpc()` 保留为兼容别名。
- `client.AcquireHTTP` / `client.AcquireGRPC`：按协议租用客户端，只返回一个客户端，协议与配置不符时返回 `client.ErrProtocolMismatch`；`Factory.AcquireClient` 不变。
- `job.WithClusterLock(locker, key, ttl)`：Cron 在共享同一 `lock.Locker` 的多个副本间每次触发只执行一次；未取得租约的实例跳过本轮（`job_triggers_skipped_total{reason="cluster_locked"}`），执行期间每 ttl/3 续租，续租失败取消本次执行，执行结束后租约保留到 TTL 过期以吸收时钟偏差。见 [集群单例](pkg/job/README.md#集群单例)。
- 客户端失败重试 `client.clients.<name>.retry`（默认关闭）：只重试 503 / gRPC Unavailable，HTTP 只重放 GET/HEAD/OPTIONS，gRPC 只重试 `operations` 列出的方法，倍增退避带抖动并受截止时间约束。见 [失败重试](pkg/client/README.md#失败重试)。
- gRPC 客户端 keepalive `client.clients.<name>.grpc.keepalive_time` / `keepalive_timeout` / `keepalive_permit_without_stream`，按需开启。
- Queue 执行期间续租 `WorkerConfig.RenewLease` 与可选接口 `queue.LeaseRenewer`（Redis Store、数据库 Store 均支持，自定义数据库 Repo 实现 `database.LeaseRepo`）：长任务可用短租约，崩溃后一个租约内即被重新领取；租约已被他人取得时取消 Handler。
- 管理端口 pprof `server.http.pprof`（默认关闭），只能挂在独立管理监听上，落到业务监听时启动失败。
- 延迟 SLO 消耗率告警 `FoundationLatencySLOBurnRateCritical` / `Warning`：默认 99% 请求在 1s 内（预算 `foundation:slo_latency_budget:ratio`），慢请求比例 `app:server_slow_ratio:rate5m…3d`，窗口与倍数同可用性 SLO；平台总览健康表新增 1h 可用性与延迟消耗率列。
- 单个下游告警 `FoundationDownstreamHighErrorRate`（5xx >5%）与 `FoundationDownstreamHighLatency`（P99 >1s），复用 `app_downstream:*` 预聚合。
- `log_events_total{event}`（`bootstrap.NewMetricsBootstrap` 注册，`log.SetEventCounter` 提供钩子）：WARN 及以上、事件名以 `.rejected` / `.panicked` / `.panic.recovered` 结尾的日志在级别过滤前计数；新增告警 `FoundationPanicRecovered`、`FoundationConfigRejected`（首次出现的计数器也会告警）。
- Alertmanager 分组、路由与抑制示例 [alertmanager.example.yaml](observability/alertmanager/alertmanager.example.yaml)，`make test-observability` 用 amtool 校验配置与路由。
- 可用性 SLO 多窗口多消耗率告警 `FoundationSLOBurnRateCritical`（1h/5m 14.4 倍、6h/30m 6 倍）与 `FoundationSLOBurnRateWarning`（1d/2h 3 倍、3d/6h 1 倍），默认 SLO 99.9%，由 `foundation:slo_error_budget:ratio` 调整；新增 `app:server_error_ratio:rate5m…3d` 预聚合及规则测试。见 [可用性 SLO 消耗率告警](observability/alerts.md#可用性-slo-消耗率告警)。
- 数据库只读副本 `database.connections.<name>.read_replicas`：事务外查询随机分发到副本，写入、事务、锁定读与 `database.UsePrimary(ctx)` 走主库；副本沿用连接的驱动、GORM、AES 与池参数（池参数一起热更新），连接池指标以 `<连接名>/replica-<序号>` 单独上报。数据库 Queue 仓储固定读写主库。见 [只读副本](pkg/database/README.md#只读副本)。
- `pkg/kafka/outbox`：Kafka 事务外盒，`Write` 在业务数据库事务内写待发消息，`Relay`（queue.Worker）提交后经 ManagedProducer 发布，至少一次、消息 ID 等于外盒 ID；复用数据库 Queue 的租约、重试、失败归档与 SKIP LOCKED 领取。见 [pkg/kafka/outbox](pkg/kafka/outbox/README.md)。
- `pkg/cache`：基于 Redis 的旁路缓存，进程内同 key 回源合并（singleflight）、TTL 抖动、可选空值缓存，经 `Config.Metrics` 复用 `metrics.CacheMetrics`。见 [pkg/cache](pkg/cache/README.md)。
- `queue.FailureCorruptPayload`：数据库 Queue 的失败列表把正文损坏的行按该分类返回（只含 ID 与时间），不再让整个列表报错。

### Changed

- `FoundationDownstreamHighErrorRate` / `FoundationDownstreamHighLatency` 改按逻辑调用（重试后）统计：重试兜住的失败不再告警，延迟包含重试与退避；未开启重试的客户端口径不变。
- 业务 HTTP 端口不再暴露 `/debug/pprof`：Kratos 把未匹配路由交给 `http.DefaultServeMux`，任何依赖导入 `net/http/pprof` 都会在业务端口上暴露剖析端点；现保留 DefaultServeMux 回退但对 `/debug/pprof` 返回 404，剖析改用 `server.http.pprof` 挂在管理端口。依赖业务端口 pprof 的部署需改用管理端口。
- Queue `WorkerConfig` 时长约束校验失败时，错误说明违反的具体约束与实际值（此前只有 `invalid queue worker concurrency or timing configuration`）。
- 数据库 Queue（`contrib/queue/database/gorm`）在 MySQL 8.0.1+ / MariaDB 10.6+ 上改用短事务内按主键 `FOR UPDATE SKIP LOCKED` 领取：并发 Worker 各自锁定不同候选，不再争抢最早的同一行。MySQL 8.4 上 8 并发领取 200 个积压任务的空领取从 1335 次降到个位数。SQLite 与不支持 SKIP LOCKED 的版本保持原条件更新方式；首次领取时读取一次 `SELECT VERSION()`。

### Fixed

- **gRPC 流式接口的 panic 会终止进程**：Kratos 的流拦截器不把中间件链套在流处理器外，grpc-go 也不恢复处理器 panic。现登记最外层恢复拦截器，把 panic 恢复为 500 `UNKNOWN` 并记录 `server.request.panic.recovered`；同时修正多个流拦截器逐个登记时互相替换的问题（Kratos `grpc.StreamInterceptor` 每次调用整体替换）。业务再传入 `grpc.StreamInterceptor` 仍会整体替换框架的流拦截器。
- 后台协程中的业务代码 panic 不再终止进程：`utils.Parallel*` 任务 panic 转为包装 `utils.ErrTaskPanicked` 的错误；配置轮询中自定义配置源 panic 时保留上一份快照（`config.poll.panicked`）；Job `ErrorHandler` panic 只记录 `job.error_handler.panicked`；直接调用 `kafka.Consumer.Consume` 时 DeliveryHandler panic 转为 Consume 错误，未提交记录下次重放；客户端后台构建 panic 转为构建失败；Server 停机附加动作 panic 转为停止错误。
- `cache.Cache.Get` 的回源函数 panic 时不再让进程崩溃（singleflight 会在独立协程中重新 panic），改为在每个等待的调用方协程中以原值重新 panic，由请求、任务或队列的恢复中间件处理。
- 数据库 Queue 的 SKIP LOCKED 探测把上报 MySQL 8 版本号的 TiDB 视为不支持，沿用条件更新领取。
- 告警 FoundationAppNotReady 与预聚合 `app:readiness:ready` 在按实例分组前把 `pod_name`（ACK 默认采集标签）补成 `pod`；此前只有 `pod_name` 时所有实例被并成一组，单个实例未就绪会被其他实例掩盖。

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- **server,client**：add HandleRoute and call-level downstream metrics（`9d00186`）
- ⚠️ 不兼容：**server**：管理端点默认独立端口 8001；业务 HTTP 默认请求体上限 32 MiB（`aff0f81`）
- **database**：SQL 日志按环境默认隐去参数；database.AutoMigrate 固定走主库（`34ed93e`）
- **observability**：延迟 SLO、单下游、panic 与配置拒绝告警及 Alertmanager 示例（`0237038`）
- **log**：log_events_total 统计 panic 恢复与配置拒绝事件（`8677f5a`）
- **database**：只读副本 read_replicas 与 UsePrimary（`35792a5`）
- **kafka**：事务外盒 pkg/kafka/outbox（`7c9fb3a`）
- **observability**：可用性 SLO 多窗口多消耗率告警（`a9b316d`）
- **cache**：基于 Redis 的旁路缓存，进程内回源合并、TTL 抖动与空值缓存（`530a46a`）
- **server**：管理端口 pprof；CHANGELOG 记录计划中的改动（`e7b2f13`）
- **queue**：执行期间续租（WorkerConfig.RenewLease）（`42190e5`）
- **client**：失败重试与 gRPC keepalive；服务端默认放宽 keepalive 检测（`206cbac`）
- **job**：WithClusterLock 让 Cron 在多副本间每次触发只执行一次（`945fb18`）

**修复**

- recover panics in gRPC streams and background goroutines（`7ad0728`）
- harden cache load panics, TiDB claim probe; add totp.VerifyAt（`01f80de`）
- **observability**：延迟 SLO 匹配 Prometheus 3 规范后的 le；告警说明映射覆盖全部告警（`66ac1e6`）
- **server**：业务端口屏蔽 DefaultServeMux 上的 pprof；默认连接级超时（`274a048`）
- 拒绝事务内切换数据库连接；失败任务列表不再被损坏行中断（`bd42260`）
- **queue**：retry transient storage errors, SKIP LOCKED claims on MySQL, pod_name-aware rules（`a8896e8`）

**重构**

- 统一 Observability 类型；kafka.NewProducerWithObservability 一步创建受管 Producer（`64ac458`）
- **bootstrap**：Spec.HTTP()/GRPC() 与 server.Spec 命名一致，Http()/Grpc() 保留为兼容别名（`f5baf65`）
- 易用性修正——Worker 配置报错说明约束、cache 指标改为 Config 字段、按协议租用客户端（`1aadeac`）

**文档**

- **changelog**：refresh v2.6.0 commit list（`0b32d2a`）
- **changelog**：prepare v2.6.0 release notes（`bc2f1ec`）
- **outbox**：stress that Write must run inside the business transaction（`ae35c53`）
- CHANGELOG 记录管理端口、请求体上限、SQL 参数等默认值变化；演示改用新接口（`eb35724`）

**测试**

- **demo**：演示缓存、事务外盒、只读副本、Cron 集群锁、续租、重试、pprof、SLO 与配置拒绝（`9dca4a8`）
- **database**：检查副本外部测试的连接关闭错误（`890bdf6`）
<!-- changelog:auto:end -->

## v2.5.0（2026-10-26）

本版本包含不兼容变更，升级前逐项确认“升级风险”；接入步骤见 [MIGRATION.md](MIGRATION.md)。

### 升级风险

- **最低 Go 版本升至 1.26**：根模块改为 `go 1.26.0`（Go 1.25 已停止安全更新，且修复 HTTP/2 服务端漏洞的 `golang.org/x/net` v0.60+ 要求 Go 1.26），依赖本框架的服务需使用 Go 1.26+ 构建。标准库漏洞只能由工具链修复，构建镜像与 CI 应使用 go1.26.10+ 或 go1.27.3+；仓库自身通过 `toolchain go1.26.10` 固定开发工具链。
- **应用类型由 `*kratos.App` 改为 `*app.App`**：App 自行实现 Run/Stop 生命周期，不再包装 `kratos.App`。`bootstrap.NewKratosApp` 更名为 `bootstrap.NewApp`；业务 injector 的返回类型改为 `*app.App` 后重新执行 `wire`，`main` 中的 `app.Run()` 不变。`bootstrap.NewMetricsBootstrap` 签名改为 `(spec, meter, pusher *metrics.Pusher, logger log.Logger) (MetricsBootstrap, error)`：使用 `BaseProviderSet` 只需重新生成；显式列出 provider 的 injector 需加入 `metrics.NewPusher`。`*app.App` 实现 `kratos.AppInfo`，`kratos.FromContext` 仍可读取身份。
- **生产环境不再返回错误堆栈**：HTTP 响应 metadata 与 gRPC `ErrorInfo` 中的 `err_stack` 仅在 `env.ExposeDiagnostics()` 为 true 时携带（local/dev/test 始终、pre 需 `APP_DEBUG=true`、prod 一律不带）。依赖跨服务远端堆栈排障的 prod 场景需改用服务端日志。见 [诊断出口环境约束](pkg/errors/README.md#诊断出口环境约束)。
- **gRPC 健康检查改为跟随应用就绪**：默认注册的 `grpc.health.v1.Health` 不再在 Server 启动后立即报 `SERVING`。空服务名与已注册业务服务名在应用就绪、未停机且 readiness 依赖检查通过时才返回 `SERVING`（`Watch` 只看就绪与停机，不跑依赖检查）；服务名 `liveness` 恒为 `SERVING`。影响与迁移：
  - Kratos gRPC 客户端默认开启客户端健康检查（`Watch` 空服务名），启动钩子完成前不再把流量发往新实例，进入停机后 1s 内摘流，随后 `stop_delay` 内完成切换；无需改动。需要旧行为的调用方可用 `kratosgrpc.WithHealthCheck(false)`。
  - 使用 K8s 原生 gRPC 探针时，liveness 须改为 `grpc: {port: <grpc>, service: liveness}`；仍用空服务名会在依赖检查失败时判定存活失败并反复重启 Pod。readiness 使用空服务名，`timeoutSeconds` 应大于 `server.http.health.timeout`。框架此前提供的部署示例使用 HTTP 探针，不受影响。
  - 已通过 `custom_health: true` 或自行注册健康服务的业务保持原实现。
- **日志事件、文案与字段统一**：全框架日志按 [日志格式约定](AGENTS.md#4-日志与可观测性) 收敛，msg 改为小写“主语+结果”短语（如 `servers assembled`、`client config rejected; keeping previous`），由 `TestFrameworkLogConventions` 持续校验。依赖下列旧名的告警、看板或检索需同步调整：

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
- gRPC 默认注册与应用就绪联动的 `grpc.health.v1.Health`（`custom_health` 或业务自行注册时让出），替代 Kratos 启动即报 SERVING 的内置实现；迁移见“升级风险”。
- App 恢复 Runtime `Start/Stop/AbortStartup` 中的 panic，记录 `app.runtime.panicked` 并统一停机（执行停止钩子与服务注销），不再直接崩溃。
- `env.ExposeDiagnostics()` 统一判断是否允许向调用方返回内部诊断，非法环境值按拒绝处理且不 panic。
- `server.middleware.updated` 增加 `changed` 字段，列出实际替换的中间件。
- `server_readiness_status` 指标：最近一次 readiness 判定（`/readyz` 或 gRPC `Check`，含依赖检查），1 就绪、0 未就绪，首次探测前不上报；监控模板新增 `app:readiness:ready` 预聚合，平台总览的“不可用 / 未全部就绪”与应用健康表据此计入依赖检查失败（`app_ready=1` 但已被 K8s 摘流量）的实例，告警口径不变。
- `appinfo.New` 支持环境变量 `APP_NAME` 覆盖应用名（默认仍为可执行文件名），用于同一二进制以多个角色部署时让日志、注册中心与指标的 `app` 区分角色。
- `test/demo/`：kind 本地集群 + kube-prometheus-stack 的多角色演示（HTTP 服务、网关、Cron、常驻进程、Queue/Kafka 消费、锁、缓存、一次性 Job），用框架真实组件产生指标，验证面板与告警。
- 可选 `metrics.push`（默认关闭）：一次性进程在应用结束（成功或失败）后的停止后钩子中，把与 `/metrics` 相同的实例 Registry 推送到 Pushgateway 一次；分组键只有 `job`（默认应用名），指标自身的 `job` 标签改为 `exported_job`，失败只记 WARN `metrics.push.failed` 且不改变退出码。新增 `metrics.NewPusher`（已加入 `BaseProviderSet`），`bootstrap.NewMetricsBootstrap` 签名增加 `pusher`、`logger`；显式列出 provider 的 injector 需加入 `metrics.NewPusher` 并重新执行 `wire`。见 [Metrics 推送](pkg/metrics/README.md#一次性进程推送到-pushgateway)。
- 监控模板新增 `foundation-kubernetes-jobs` 告警组（基于 kube-state-metrics）：`FoundationJobFailed`、`FoundationCronJobMissedSchedule`、`FoundationCronJobNoRecentSuccess`、`FoundationJobRunningTooLong`，及规则测试；`app` 标签需 kube-state-metrics `metricLabelsAllowlist` 放开 `app.kubernetes.io/name`，示例见 `observability/kubernetes/kube-prometheus-values.yaml`。

### Changed

- 系统信号与父 Context 取消从 `Run` 开始即转为统一停止，启动阶段收到 SIGTERM 也会执行停止钩子与启动前回滚。Runtime 的真实故障全部按发生顺序汇总返回（根因在前）。
- `app.StopPolicy` 在订阅通知时即时校验并记录 `app.stop_policy.updated`（新增 `previous`）/ `app.stop_policy.rejected`，解码失败也记为后者；不再输出 `version`。
- `config.NewHotReloadValue` 改为先加载初值再登记订阅，依赖 Manager 下一轮回放覆盖期间的更新。
- `database.WithSQLTxOptions(nil)` 表示驱动默认选项，嵌套事务可接受；`Transaction` 不再为 nil 回调或 nil 选项返回错误（编程错误直接 panic）。
- 日志热路径：caller 按调用地址缓存帧解析结果，字段过滤、module 归一化、格式化、去空与去重合并为一遍处理，输出内容不变；启用级别下单条日志开销约降低 6 倍。
- Client：已构建版本的 `AcquireClient` / release 走无锁分片计数快速路径，16 并发下约 6 倍吞吐，租约与退役语义不变。
- HTTP 监听、Queue Worker、Kafka ConsumerRuntime 与错误树判断改为共享的精简实现，外部行为不变。
- 生成器模块（`cmd/protoc-gen-*`）升级依赖：errors-v2 的 grpc 1.61→1.84、x/sys，jsonschema 的 x/text 0.22→0.43 等修复已知漏洞；client-v2 由已拆分前的 `google.golang.org/genproto` 改用 `genproto/googleapis/api`，依赖图从数十个模块降至 10 个。生成产物不变。errors-v2 仅导入 Kratos 的错误扩展定义，GO-2026-5471（`transport/http`）不可达。
- 仓库结构调整（不影响 Go API）：`deploy/` 拆分为 `observability/`（Grafana 面板、Prometheus 规则与接入说明）与 `test/`（本地可运行的外部服务测试、面板校验与演示环境）；`examples/` 只保留用法代码；`MIGRATION_V2.md` 精简为 [MIGRATION.md](MIGRATION.md)，删除 `PROJECT_VISION.md`、`PACKAGE_REVIEW.md`；开发工具通过 `make init` 安装到 `tools/` 并锁定版本，新增 `make help`、`make vulncheck`、`make changelog`。
- 依赖安全升级：grpc 1.84、OpenTelemetry 1.47（Prometheus exporter 0.69，导出的指标名与标签经比对不变）、golang.org/x/net 0.61、x/crypto 0.58、x/text、x/sys、pgx 5.11、ch-go 等升级到不受已知漏洞影响的版本，`govulncheck` 在 go1.26.10 下无可达漏洞。go-redis（9.23 在建连时新增 DNS 解析）、franz-go（1.22 改变主题重建与缓冲默认值）、fsnotify（1.10 在 macOS 下热重载偶发 bad file descriptor）均无已知漏洞，为避免行为变化暂保持原版本。剩余未修复公告：Kratos GO-2026-5471（`transport/http`，无上游修复，代码未调用受影响符号）与 x/crypto `openpgp` 包弃用公告（未导入）。
- Kafka `ManagedProducer` 发布失败的 WARN 日志增加 `error` 字段（如 `UNKNOWN_TOPIC_OR_PARTITION`、认证失败），franz-go 错误不含消息正文或 Header。
- Grafana 面板（演示环境实测后修正）：平台总览数据库 / Redis 实例表先按实例聚合，修复同一实例被拆成多行；当前告警表排除 Watchdog、InfoInhibitor 与 none/info 级别，显示说明与已持续时间；健康表按未就绪实例数降序；表格空单元格统一显示“—”，环境与集群列移到末尾；网关页变量由 `gateway` 改名为 `app` 并改为单选，服务详情跳转可直接带入应用，入口指标不再默认汇总所有有下游调用的应用；容器页 Running / Ready Pod 与容器总数不再计入已结束的 Job Pod；应用视图（Pod / 容器 / 实例）在已按命名空间分组的图例中不再重复命名空间；服务详情抓取状态时间线不再绘制重叠的值文本；面板检查新增“多查询合并表格不得使用保留 `__name__` 的裸选择器”。
- WebSocket 停机关闭与 HTTP `Shutdown` 并行执行、共享同一预算，卡住的 WebSocket 回调不再推迟 HTTP 停止接收新连接或挤占在途请求的优雅完成时间；停机关闭帧由 1000 改为 1001 Going Away（业务主动 `Close()` 仍为 1000），依赖关闭码判断是否重连的客户端需同步调整。

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- readiness 状态指标、APP_NAME 应用名覆盖与 Kafka 发布错误日志（`f3f00f6`）
- **observability**：platform overview with templated detail dashboards（`23b1f92`）
- **observability**：add Kubernetes Job alerts and optional Pushgateway push（`aee73b3`）
- **observability**：add fleet recording rules and overview dashboard（`68bf5a2`）
- harden runtimes, gRPC health and readiness observability（`83df748`）
- **errors**：return err_stack only outside production（`0403f8c`）

**修复**

- **observability**：按演示环境实测修正平台总览与各详情面板（`dd93cb4`）
- **server**：stop WebSockets alongside HTTP shutdown and send 1001（`caccaca`）
- **validation**：reject CGNAT, multicast and reserved ranges in IsSafeIP（`0d28b4a`）

**性能**

- **log**：cache caller frames and fuse the field pipeline（`396c008`）
- **client**：lock-free lease fast path for built clients（`ed69c81`）

**重构**

- ⚠️ 不兼容：**log**：unify log events, messages and fields（`c841cb0`）
- remove redundant defensive code（`9763fc2`）
- ⚠️ 不兼容：**config**：simplify hot reload and consolidate change logs（`9bcd9b5`）
- ⚠️ 不兼容：**app**：own the application lifecycle and expose *app.App（`f3dcdd2`）
- **server**：simplify managed listener and build wrappers once（`cc9a915`）
- **lifecycle**：share single-run start/stop coordination（`ce7b01e`）
- **errtree**：unify cancellation-only error tree checks（`10cacf2`）

**文档**

- **changelog**：refresh v2.5.0 commit list（`85bc84f`）
- **changelog**：prepare v2.5.0 release notes（`e1cac12`）
- ⚠️ 不兼容：restructure documentation, layout and development tooling（`e94add4`）
- **changelog**：record WebSocket shutdown changes（`20947b6`）
- **changelog**：record planned foundation review changes（`08450ee`）

**测试**

- **demo**：kind 本地多角色演示环境与 changelog（`56b8d17`）
- align Consul and MySQL external tests with documented semantics（`250c849`）
- **server**：verify Kratos client health checking across the lifecycle（`4c381b7`）
- cover composed lifecycle, hot reload and startup signals（`95d8169`）
- merge scenario-suffixed test files into their implementation tests（`7ff4302`）

**构建与依赖**

- **cmd**：upgrade generator module dependencies（`73635fa`）
- **cmd**：align generator modules with Go 1.26 and the root toolchain（`e45d79d`）
- ⚠️ 不兼容：**deps**：require Go 1.26 and upgrade vulnerable dependencies（`7e1b3ae`）
<!-- changelog:auto:end -->

## v2.4.2（2026-10-06）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- **queue, job**：add debug query sessions and improve job lifecycle logs（`693a6a6`）
<!-- changelog:auto:end -->

## v2.4.1（2026-10-02）

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

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- add null registry driver and inherit tracing defaults（`e8f3368`）
<!-- changelog:auto:end -->

## v2.4.0（2026-09-30）

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

API 与配置迁移细节见 [迁移指南](MIGRATION.md)。按事件名检索或告警的日志查询需要替换为新名称：

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

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- unify log events and harden startup lifecycle（`26d6bc1`）

**文档**

- prepare v2.4.0 changelog with upgrade risks（`6babe73`）
<!-- changelog:auto:end -->

## v2.3.3（2026-09-23）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**修复**

- improve RPC and configuration diagnostics（`6da3c70`）
<!-- changelog:auto:end -->

## v2.3.2（2026-09-23）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- **log**：normalize messages and remove redundant function fields（`90c3d70`）
<!-- changelog:auto:end -->

## v2.3.1（2026-09-23）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- **consulconfig**：require business config prefixes and paths（`1b3d143`）
<!-- changelog:auto:end -->

## v2.3.0（2026-09-23）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- **consulconfig**：provide default remote config directories（`49acf50`）
- **bootstrap**：support templated configuration paths（`f3bc19c`）
- **observability**：redesign ACK container and node dashboards（`a8741aa`）
<!-- changelog:auto:end -->

## v2.2.1（2026-09-21）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- **app**：增加独立的服务注册禁用开关（`c238112`）
<!-- changelog:auto:end -->

## v2.2.0（2026-09-21）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- add utility helpers and configurable input validation（`e8d847a`）

**重构**

- **config**：优化配置变更路径前缀构建（`933b284`）
- **config**：精简订阅日志并记录订阅调用点（`d504bd2`）
<!-- changelog:auto:end -->

## v2.1.5（2026-09-18）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- 统一路由注册与 SQL 日志上下文（`acc7ccf`）
<!-- changelog:auto:end -->

## v2.1.4（2026-09-18）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**重构**

- **server**：统一自定义 HTTP 响应编码（`421b27f`）
<!-- changelog:auto:end -->

## v2.1.3（2026-09-18）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- **server**：支持自定义 HTTP 端点与 WebSocket 并发（`3f060d6`）
<!-- changelog:auto:end -->

## v2.1.2（2026-09-18）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**修复**

- **http**：恢复 v1 JSON 序列化契约（`b2ad372`）

**重构**

- flatten middleware configuration（`e93f38f`）

**文档**

- design flattened middleware config（`dc3abdb`）
<!-- changelog:auto:end -->

## v2.1.1（2026-09-18）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**修复**

- preserve v1 v2 transport compatibility（`4b10a8c`）
<!-- changelog:auto:end -->

## v2.1.0（2026-09-17）

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

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- prepare v2.1 operations and observability（`939c363`）
- **config**：在热更新日志中标明变更路径和订阅身份（`7dad5e8`）
- **job**：支持通过 disabled 动态禁用 Cron 任务（`41afec3`）
- **queue**：支持保留任务执行记录并优化数据库查询（`a88f5c9`）
- **log**：新增根级别配置并作为输出端默认级别（`588ae9d`）
- **config**：支持自定义远程配置名称并记录加载路径（`551165e`）
- **config**：为订阅热更新添加 Info 日志（`d82e813`）
- **queue**：提供类型化接入与数据库简单模式（`13d1ca7`）
- **consulconfig**：支持自定义远程配置名称 provider（`aaf95dc`）
- **client**：支持根级超时与服务发现默认配置（`6b4a9f9`）
- **bootstrap**：提供 Consul 默认配置路径组装集合（`fbd9a52`）

**修复**

- **log**：标准输出默认保留 service 字段（`bc49ef7`）
- 补充驱动注册日志并抑制连接池重复配置日志（`b49caa6`）
- **log**：将 module 展示在时间戳和 caller 之间（`e655985`）

**重构**

- **queue**：infer message versions and clarify struct contracts（`db57487`）
- **job**：移除跨进程协调并支持调度配置热更新（`9578514`）
- **bootstrap**：统一配置来源组装与路径优先级（`bcf563c`）
- **queue**：统一类型化队列 API 并整理文件职责（`2471936`）
- **bootstrap**：简化 Spec 蓝图组装与协议启用规则（`9c45d00`）
- **consulconfig**：将远程配置路径函数归入 spec（`3358f5a`）
- **bootstrap**：重命名 consulconfig 并导出远程配置路径函数（`e56ae53`）
- 简化配置组装并完善运行日志（`2f08e00`）
- ⚠️ 不兼容：统一配置源组装、Registry 驱动与动态日志策略（`6a954b4`）
- ⚠️ 不兼容：拆分配置组装并统一路径与日志语义（`39fc005`）
<!-- changelog:auto:end -->

## v2.0.0（2026-09-14）

<!-- changelog:auto:start -->
### 提交记录

> 由 `make changelog` 按 Git tag 生成，请勿手改本块。

**新增**

- ⚠️ 不兼容：重构持久化任务队列并完善组件监控与示例（`3286f77`）

**修复**

- **database**：skip AES decryption for empty values（`b99a367`）
- **errors**：兼容旧协议错误并保留服务间诊断链（`ab84415`）
- **log**：单元测试默认禁用文件日志（`144fc32`）
- ⚠️ 不兼容：**log**：resolve caller through adapters and simplify depth API（`78dcfdd`）

**性能**

- 优化基础组件热点并完善资源边界与外部验证（`c66ef5e`）

**重构**

- 通过 Wire 构造注入注册中心和任务协调器（`7de373d`）
- ⚠️ 不兼容：统一 bootstrap 组装与日志资源所有权（`e28a1d9`）
- ⚠️ 不兼容：重构 v2 组装边界与资源生命周期（`cac93a9`）

**文档**

- 修正文档契约与接入示例并补充防漂移规范（`888ed74`）

**其他**

- 忽略应用与测试产生的 app.log（`4560ac2`）
- Refactor `Makefile` and update serviceName logic in `protoc-gen-kratos-foundation-client`.（`4fd91b2`）
<!-- changelog:auto:end -->
