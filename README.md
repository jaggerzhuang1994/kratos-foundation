# kratos-foundation

`kratos-foundation` 是基于 [go-kratos](https://github.com/go-kratos/kratos) 的 Go 应用基础库，统一组装日志、配置、服务端、客户端、数据库、缓存、队列、任务与可观测性能力。

> 项目尚未正式发布，公开 API 仍可能在首个稳定版本前调整。

## 环境要求

- Go 1.25+
- 使用 Protocol Buffers 或 Wire 生成功能时，需先安装对应工具链

## 业务模板与监控部署

[最小业务模板](examples/minimal/README.md) 提供可复制的 HTTP/Wire 应用、配置、健康检查与业务指标示例。
[监控部署入口](deploy/observability/README.md) 提供本地 Docker Compose、普通 Prometheus 采集配置及可导入的 Grafana Dashboard；
[Kubernetes 示例](deploy/kubernetes/README.md) 接入已有 kube-prometheus-stack，通过 ServiceMonitor 和 PrometheusRule 复用同一面板与规则。

面板支持环境、集群、命名空间、App、机器/Node、Pod、实例和接口筛选；应用图可按 App/Pod/实例/Node 聚合。
整机指标需额外的 node-exporter，和应用进程指标分开解释。详见 [维度说明](deploy/observability/docs/dashboard.md)、
[告警接入](deploy/observability/docs/alerts.md) 和 [排障手册](deploy/observability/docs/troubleshooting.md)。

## 配置参考

- [`config.example.yaml`](config.example.yaml)：当前 Foundation YAML 配置参考，包含可选字段的注释示例。
- [`config.schema.json`](config.schema.json)：编辑器补全与结构校验；由 [`proto/config.proto`](proto/config.proto) 及其导入协议生成，不能手工修改。
- [`pkg/config`](pkg/config/README.md)：配置源合并、环境变量替换、类型解码、订阅及错误语义。

示例覆盖未弃用的协议字段；连接级 `database.connections.*.gorm` 与全局 `database.gorm` 共用字段，示例只列出部分覆盖值，完整字段看全局块。弃用的 `database.tracing.exclude_metrics` 仅保留说明，数据库指标统一使用 `database.metrics`。

示例值不等于默认值：例如 `server.stop_delay: 3s`、客户端超时和 Redis 连接池大小都是显式设置。注释中的 `[默认: ...]` 说明省略字段后的行为；显式 `0`、`false` 或 `0s` 是否等同省略由具体字段决定。Duration 使用 protobuf 秒字符串，如 `10s`、`0.2s`、`0s`，不能使用 `10m` 或裸数字。Redis 的负时长哨兵 `-1ns`、`-2ns` 分别写为 `-0.000000001s`、`-0.000000002s`，不是 `-1s`、`-2s`。

| YAML 顶层配置 | 用途 | 支持热更新的范围 |
| --- | --- | --- |
| `log` | 根过滤及标准/文件输出策略 | level、filter_keys、modules、std、file 全部字段；禁用和格式固定于 env |
| `app` | 注册端点、metadata、注册与停机期限 | 仅 `stop_timeout`；停机开始后预算固定 |
| `tracing` | OTLP 导出器与采样器 | 仅 `sampler`；构造时已禁用的 Provider 不能靠热更新启用 |
| `server` | HTTP/gRPC、中间件、健康与指标端点 | 仅 `middleware`；地址、端点和停机延迟需重启 |
| `registry` | 具名注册与发现实例；驱动选项含健康检查、心跳、标签及发现超时 | 无；实例集合与选项需重启 |
| `database` | 具名连接、GORM、连接池及观测 | 仅各连接的 `max_idle_conns`、`max_open_conns`、`conn_max_lifetime`、`conn_max_idle_time` |
| `redis` | 具名连接、追踪与指标 | 无 |
| `client` | 具名服务客户端、调用中间件与清理预算 | 仅 `clients`；`cleanup_timeout` 需重启，日志由 log.modules 管理 |
| `kafka` | 具名 broker 连接、TLS/SASL、生产/消费参数 | 无 |
| `oss` | 按逻辑名配置 bucket 和驱动参数 | 无 |
| `job` | 已注册 Cron 的调度与本进程并发策略 | disabled、schedule、concurrent_policy、max_pending_runs；run_immediately 只在启动时触发 |

热更新按组件生效，不能视为整份应用配置同时切换。Server 中间件会先校验组合再逐项发布；Database 同次更新若含需重启字段，会跳过整次数据库更新，连接池参数也不应用；Client 的日志或 cleanup 预算变更只提示需重启，不阻止有效的 `clients` 更新。配置 Manager 接受快照也不代表每个组件都已应用，具体边界见各包 README。

### 加载与部署

框架不会自动寻找 `config.example.yaml`。应用将需要的配置复制到部署文件，替换地址、连接名和凭据，并通过 [`contrib/config/file`](contrib/config/file/README.md) 的 `NewSources(logger, PathList{...})` 或 [`contrib/config/consul`](contrib/config/consul/README.md) 创建源，再交给 `config.NewManager`。只有应用显式构造的组件才会消费对应配置块；删除数据库或 Redis 配置时，也应调整相应组件的组装。

Manager 默认每秒 Scan 一次完整配置并串行通知变化，可用启动环境变量 `CONFIG_POLL_INTERVAL` 调整；支持缺失 key 与同 key 多订阅。Load 读取最近扫描快照。Sources 初次按传入顺序加载，更新采用 Kratos 默认 merge；不保证删除回退、固定来源优先级或 null 清空。`$VAR` / `${VAR}` 在原始 KeyValue 的 JSON/YAML 解析前通过 Compose 模板替换环境变量，支持默认值和必填检查；普通变量未设置时为空。合并后保留官方 resolver；业务源使用 `$${key:default}` 将配置引用留到第二阶段。具体边界见 [环境变量模板](pkg/config/README.md#环境变量模板)。凭据应由实际部署配置源或受信任的进程环境提供，示例中的占位值不能用于连接真实服务。

```mermaid
flowchart TD
    A([应用组装配置源]) --> B[文件源 或 Consul KV 外部配置源]
    B --> C[NewManager 加载并合并快照]
    C --> D{加载 解码 占位符与已删除字段检查通过?}
    D -- 否 --> E[释放配置源 返回构造错误]
    D -- 是 --> F[组件 Load 合并默认值并校验配置]
    F --> G{组件配置有效?}
    G -- 否 --> H[返回构造错误 Wire 逆序 cleanup]
    G -- 是 --> I([交给后续组件构造与应用组装])
    E --> J([启动失败])
    H --> J
```

以下能力不属于 Foundation YAML 顶层协议，不能把旧字段补回示例：

| 能力 | 当前配置入口 |
| --- | --- |
| 应用环境 | `APP_ENV`，其次 `KRATOS_ENV`，均未设置时为 `local`；见 [`pkg/env`](pkg/env/README.md) |
| 根日志 | `log` 配置动态控制策略，`LOG_*` 提供启动默认值与输出资源配置；模块策略统一集中到 `log.modules` |
| Consul 地址与认证 | 配置源与注册发现共享 env 驱动的进程单例，见[内部生命周期](internal/consul/README.md) |
| Metrics Provider | 显式构造和注入；HTTP 暴露位置仍由 `server.http.metrics` 配置 |
| Job、Queue | Job 的 `job.cron` 支持热更新；Queue 使用强类型构造配置及显式 Bootstrap |

配置协议不保留已删除字段的名称或编号；旧配置不再依赖 reserved 校验拒绝，迁移时应主动清理，详见 [配置迁移表](pkg/config/README.md) 和 [v2 迁移清单](MIGRATION_V2.md)。

### 健康检查与配置观测

默认业务 HTTP 端口提供 `/metrics`、`/healthz` 和 `/readyz`。`/healthz` 表示 HTTP 能响应；`/readyz` 在全部启动后钩子完成、关键依赖检查通过且未停机时返回 200，否则返回 503。`bootstrap.NewServerBootstrap` 自动绑定应用就绪状态；直接使用 Runtime 时需要显式绑定，否则 readiness 保持 503。

设置 `server.http.metrics.addr` 和 `server.http.health.addr` 可独立监听，例如同时设为 `127.0.0.1:9001` 会共享一个管理监听。空地址复用业务 HTTP；业务 HTTP 禁用时，管理端点只有显式设置地址才会启动。管理端点独立于业务路由前缀、Filter 和鉴权；独立监听使用普通 HTTP，不继承业务 TLS，也不进入业务服务发现。完整地址规则、探针流程及停机边界见 [`pkg/server`](pkg/server/README.md)。

业务 HTTP 默认开启，gRPC 按有效服务注册默认开启；显式 `server.http.disable` / `server.grpc.disable` 优先，修改需要重启。`spec.Http()` 仅声明业务 HTTP，不控制独立管理监听。依赖检查通过 `spec.Health().Checks(...)` 追加；健康端点地址、路径、开关和总检查期限统一由配置管理，不提供代码覆盖入口。

配置来源加载、解析与监听沿用 Kratos 官方日志；Manager 不再提供自定义配置健康状态或配置观测 collector。

## 构造依赖约定

Wire 或手工组装层负责提供非空的必需组件依赖（如 Config Manager、Logger、AppInfo、遥测 Provider、Spec 和 Runtime）。构造函数及 Bootstrap 不重复检查这些依赖是否为 `nil`；直接调用时也必须遵守该前置条件。配置、外部输入、回调以及来源不确定的返回值继续按各包契约校验。显式支持禁用的可选依赖（如 Consul Client、Registrar、Discovery ）保留 `nil` 语义。

## 核心组装流程

Wire 通过 `app.NewSpec`、`server.NewSpec`、`job.NewSpec` 构造共享声明并注入各依赖处；`bootstrap.NewSpec` 接收这三个实例和具体的 ConfigSources 描述。`bootstrap.Spec` 作为业务蓝图，声明方法返回同一 Spec，可链式调用；组装顺序由 Wire 的依赖链保证。`RegisterRuntime` 直接写入共享的 `app.Spec`，不等待后续阶段重放。底层 app.Spec 登记方法无返回值，冻结后写入直接 panic；契约见 [bootstrap](pkg/bootstrap/README.md) 和 [app](pkg/app/README.md)。

```mermaid
flowchart LR
    A[Wire: app/server/job NewSpec 注入 bootstrap.NewSpec；app.NewConfig] --> B[构造组件]
    B --> C[bootstrap.NewXXXBootstrap 同步贡献]
    C --> D[bootstrap.InfrastructureBootstrap]
    D --> U[业务立即登记 Runtime 并提供 bootstrap.Bootstrap]
    U --> V[bootstrap.StartupReady]
    V --> E[bootstrap.NewKratosApp 调用 app.NewApp 冻结 Spec]
    E --> F[application.Run 启动 Runtime]
    F --> G[Wire cleanup 逆序释放资源]
```

`pkg/bootstrap` 集中提供各组件的 `XXXBootstrap` 与 `NewXXXBootstrap`，领域包只提供声明自身依赖的普通构造函数；`pkg/app` 只定义应用依赖与构造函数。Wire 按 `InfrastructureBootstrap → Bootstrap（业务提供）→ StartupReady → NewKratosApp` 分阶段；业务 provider 显式依赖基础设施完成标记，阶段内不规定额外顺序。Bootstrap 只在构造期同步组装；Runtime 仅在 `application.Run()` 时启动。

`BaseProviderSet` 统一构造配置源链与具名注册/发现实例，并为队列提供复用应用观测依赖的 `queue.Observability`。 默认 local/Consul 配置选择使用 [consulconfig.ProviderSet](contrib/bootstrap/consulconfig/README.md)，由 `bootstrap.NewSpec` 登记加载器。应用显式提供 AppInfo、LocalConfigPath 和 RemoteConfigDirName；RemoteConfigName 默认来自 AppInfo.Name()，可选择 `ProviderSetWithCustomRemoteConfigName` 并注入自定义名称；远程环境路径为 `{dir}/{env}/{name}.yaml` 和 `{dir}/{name}/{env}/*.yaml`。默认十二层远程路径与本地文件/目录/glob 规则在 contrib 实现。

## 日志

`pkg/log` 提供：

- 基于 `LOG_*` 环境变量的严格配置解析。
- stdout/stderr 分流与可轮转文件输出。
- 日志级别：请求 debug > log.modules 首项命中级别 > WithLevel > log.level > LOG_LEVEL；格式在启动时固定，输出端策略支持热更新。
- 每次 `NewLogger` 构造独立输出并返回 cleanup；Bootstrap 订阅 log 配置更新过滤与输出资源，根级别支持热更新并作为 std/file 级别默认值，优先于 env；格式固定于 env；`log.WithModule` 返回借用当前输出的派生视图。
- 不可变的模块、上下文、级别和敏感字段派生。
- 每条输出保留有效 `module`，缺失时为 `unknown`；通过 Foundation 安装全局绑定后，Kratos SDK 日志归属 `kratos`。

完整用法、配置表和设计边界见 [`pkg/log/README.md`](pkg/log/README.md)。

## Database 与 OSS 驱动注册

Database、OSS 和 Registry 满足“一个 Manager 管理多份具名资源，并按配置选择不同驱动”的条件，因此使用 `init + frozen registry`。业务/Wire 通过空导入明确决定哪些驱动进入最终二进制；驱动的 `init` 只注册 factory，不读取配置或创建外部资源。

Database 可同时编译 MySQL 与 SQLite3，具体连接由 `database.connections[*].driver` 选择：

```go
package assembly

import (
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/mysql"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/sqlite"
)
```

OSS 默认提供阿里云实现，bucket 由 `oss.buckets[*].driver: aliyun` 选择：

```go
package assembly

import _ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/oss/aliyun"
```

注册完成后，组装层只调用公共的 `database.NewManager(...)` 和 `oss.NewManager(...)`。驱动注册表在首次构造 Manager 时冻结；需要新增 PostgreSQL、七牛云等实现时，应增加对应 `contrib/<domain>/<driver>` 包，而不是把具体 SDK 放进领域包。

## 任务队列与 Kafka 消息

`pkg/queue` 提供类型化投递对象 Queue[T] 和消费运行时 Worker[T]，业务只依赖消息及自身发送接口；底层保留持久化任务契约，Redis 后端由 `contrib/queue/redis` 构造，Database 后端由 `contrib/queue/database` 适配业务提供的 Repo（使用 TaskRecord 交换任务及执行状态，可选 GORM 简单模式或自定义模型 Repo，支持与业务数据在同一本地事务中写入任务；GORM 可通过 [RetainCompleted](contrib/queue/database/gorm/README.md#保留执行记录) 保留成功记录及执行状态）。支持即时/延迟投递、租约恢复、持久化重试、失败查询与人工重试；业务 Handler 仍须幂等。应用入口将 Worker 登记到同一个 `app.Spec`，由应用统一启停，随后释放存储连接。完整配置、运行示例和失败边界见 [Queue 文档](pkg/queue/README.md)。

`pkg/kafka` 独立提供客户端工厂、消息生产者、消费者及消费运行时，适合事件传播与消费组处理；不作为任务队列后端。构造、批量发送、offset 提交、重试/死信和资源所有权见 [Kafka 文档](pkg/kafka/README.md)。两者使用显式注入，不进入全局 Driver Registry。

```mermaid
flowchart LR
    A([业务组装]) --> B{运行能力}
    B -->|持久化任务| C[queue Worker 与 Queue + Redis或Database Store]
    B -->|Kafka消息| D[kafka ConsumerRuntime]
    C --> E[显式登记到app.Spec]
    D --> E
    E -- 登记失败 --> F([返回错误并释放已构造资源])
    E -- 成功 --> G[应用启动和停止运行时]
    G --> H([运行时退出后释放存储或Kafka客户端])
```

## Job 调度与并发

Job 仅提供本进程、同一 Manager 内的 AllowOverlap、SkipIfRunning、DelayIfRunning。`job.cron` 按注册名称热更新启用状态（disabled 默认 false）、表达式、并发策略和等待容量；run_immediately 只在启动时触发。除仅由配置控制的 disabled 外，四项调度参数按配置、注册声明、Task 默认值的顺序解析；重新启用不补跑立即执行，详见 [Job 文档](pkg/job/README.md)。Bootstrap 注入 config.Manager，无需 Coordinator provider。


## 主要目录

完整包分类见 [`pkg/README.md`](pkg/README.md)，新增工具、契约、资源组件、Runtime、Bootstrap 或第三方适配时，参考[包开发模式指南](pkg/DEVELOPMENT.md)。指南包含 watchdog 看门狗锁的包归属、生命周期与开发示例。

```text
api/                              对外 Protocol Buffers 定义及生成产物
pkg/<domain>/                     公共契约、构造入口与同领域实现
pkg/<domain>/internal/<capability>/ 按需拆出的独立私有子能力
contrib/<domain>/<driver>/        业务 App/Wire 可选择的公共第三方实现
internal/<capability>/            至少由两个领域直接复用的私有能力及测试设施
proto/                            仓库内部 Protocol Buffers 定义及生成产物
```

例如，Tracing 的配置、Exporter 与 Sampler 按文件组织在 `pkg/tracing`，进程信息采集位于 `pkg/appinfo/appinfo.go`。日志输出、配置解码、队列遥测以及 client/server 专属中间件具有独立职责，保留所属领域的嵌套 `internal`。优先用非导出标识符隐藏实现，不增加纯转发层。

手写实现文件通常控制在 150–300 行，超过 400 行检查职责是否需要拆分；不为行数或目录一致性机械拆包。Wire 仍通过公开构造函数创建依赖并持有 cleanup，Bootstrap 在构造期贡献 `app.Spec`，`app.NewApp` 在最终屏障后冻结并组装应用。

## v2 迁移与发布

从 main 升级时，按 [v2 迁移与发布清单](MIGRATION_V2.md) 核对接口、配置、生成器和业务验收。
`make lint` 覆盖全部 Go 模块；`make test-business` 验证 Wire、HTTP/SQLite 订单事务和生成客户端契约。
发布前运行 `make verify-release`，并完成清单中的业务环境验收。该命令不会推送、打 tag 或部署。

## 开发与验证

```bash
make test
make vet
make race
make lint
```

一次执行不需外部基础设施的完整验证：

```bash
make verify
```

修改 Protocol Buffers 或 Wire 等生成源后，使用仓库对应的 `make proto` 或 `make generate` 目标，不要手工修改生成文件。

本地 Docker 真实服务验证：在仓库根目录运行 `make test-external`，自动创建并清理隔离的 MySQL/Kafka/Redis/Consul，执行集成、恢复和批量基准。服务版本、端口、产物及适用边界见 [外部测试说明](testdata/external/README.md)。

完整组件与真实指标演示见 [components 示例](examples/components/README.md)，保留最小模板的轻量接入方式。


### 旧错误兼容与安全请求日志

默认 Server 请求链与 HTTP 编码器统一归一化旧结构化错误，保留原始 HTTP 状态及业务码，保留服务间 gRPC 堆栈及 cause 诊断，过滤响应头；HTTP 公开输出仍屏蔽堆栈。关闭访问日志仍保留一次服务端故障诊断；访问摘要不包含请求或响应正文。适用边界与流程见 [Server 错误边界](pkg/server/README.md#请求错误边界与安全日志) 和 [错误兼容](pkg/errors/README.md)。

应用级请求 debug 使用 [`request.WithDebug(ctx)`](pkg/request/README.md)，日志消费该状态，HTTP/gRPC 传输层按配置跨服务传播。

## 驱动组装入口

应用通过 `spec.Configuration` 声明额外来源，由 `bootstrap.NewConfigManager` 构造默认包含官方 env source 的配置源链，使用 `registry.NewFactory` 管理具名注册与发现实例，由 `bootstrap.BaseProviderSet` 完成组装。注册与发现仅提供驱动入口。详见[驱动组装与迁移](pkg/registry/README.md)。
