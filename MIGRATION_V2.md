# main → v2 迁移与发布清单

本文对应 main `4fd91b2` 到 v2 的公共接口迁移。适用于应用接入方和框架发布者；
迁移完成意味着应用完成配置和 Wire 改造、重新生成协议代码，并通过本文的业务验收。
仅改 import 前缀不构成迁移完成。

## 资源容量默认值

- WebSocket 旧注册入口现在默认限制传输载荷及解压后消息为 1 MiB。需要其他大小时使用 `WebSocketWithConfig`；`MaxMessageBytes: -1` 显式恢复旧的无限制行为，`0` 使用默认值。见 [Server 文档](pkg/server/README.md)。
- 两种 Cron Delay 策略现在默认每任务每进程允许最多两个调用进入执行权竞争（本地为一轮执行、一轮等待）；超额触发跳过并告警。通过 `WithMaxPendingRuns` 调整，`-1` 恢复无界等待。AllowOverlap 和 Skip 不变。见 [Job 文档](pkg/job/README.md#delay-容量)。
- Deadline 前缀改用只读索引，匹配与更新失败回退语义不变，无需迁移路由配置。

## 驱动与配置源优化兼容性

OSS 的不同逻辑 bucket 现在可以并发调用 factory；自定义驱动必须并发安全并为外部调用设置超时。同名创建仍合并，cleanup 等待已受理创建完成后关闭实例，业务先停止使用再释放。并发及释放流程见 [OSS 文档](pkg/oss/README.md#构造与释放)。

内置 file、Consul watcher 显式声明完整快照，更新时不再重复 Load。第三方 watcher 无需修改，默认继续重新 Load；仅在满足完整、有序、空结果表示全部删除及缓冲所有权要求时声明 `FullSnapshot() bool`。契约及发布流程见 [配置文档](pkg/config/README.md#watcher-完整快照契约)。Kafka 批量和并发默认值、同步提交语义不变。Broker 重启可能产生的首读 EOF 现在最多额外重建三次，成功提交后重置预算；明确认证/授权失败仍终止，见 [Kafka 恢复说明](contrib/queue/kafka/README.md#断线与消费恢复)。

## 迁移清单

| 检查 | main 用法 | v2 操作与验收 |
|---|---|---|
| [ ] 工具链 | Go 1.24.12 | 使用根 go.mod 要求的 Go 1.25+；SQLite 验证需要 CGO 和 C 编译器 |
| [ ] 模块路径 | `github.com/jaggerzhuang1994/kratos-foundation` | 改为 `github.com/jaggerzhuang1994/kratos-foundation/v2`，执行 `go mod tidy` |
| [ ] 组装入口 | 领域包中的 Wire/Bootstrap、根 provider set | 使用 `pkg/bootstrap` 和公开构造函数；不得导入领域 internal；重新运行业务 Wire |
| [ ] 组装顺序 | App 直接依赖多个领域 Bootstrap | `InfrastructureBootstrap → Bootstrap → StartupReady → NewKratosApp`；业务 provider 显式依赖基础设施标记 |
| [ ] 资源所有权 | 各组件 release/关闭方式不一致 | 保留构造函数返回的 cleanup；先停止 App，再由 Wire 逆序清理；启动失败检查回滚 |
| [ ] 应用身份 | `pkg/app_info`、GetId/GetName 等 | 使用 `pkg/appinfo` 的 ID/Name/Version/Metadata；检查注册信息与日志身份 |
| [ ] 配置源 | NewConfig 根据环境安排文件/Consul 优先级 | 用 `config.NewSources` 显式排序，后面的源优先；使用 `contrib/config/file/consul/text` |
| [ ] 配置读取 | Kratos Config/Value/Watch 调用 | 改为 `config.Manager.Load/Subscribe`；回调对象独立，取消订阅不等待正在执行的回调 |
| [ ] 订阅故障 | 业务可能忽略 watcher 错误 | 处理 `ErrObserverOverloaded`、`ErrWatcherStopped`；记录告警并决定重建组件或重启；终止后不会自动恢复订阅 |
| [ ] 客户端构造 | 旧 Factory 构造及 ResolveClient/MakeGrpcConn/MakeHttpClient | 使用新的 NewFactory 依赖签名；`AcquireClient(ctx, name)` 成功后必须 `defer release()` |
| [ ] 连接选择 | WithDefaultConnName/WithConnName | 生成客户端用 `NewXxxWithConnName(factory, name)`；手写调用显式传连接名 |
| [ ] 调用选项 | GrpcCallOptionFromContext/HttpCallOptionFromContext 等 | 改用 WithGRPCCallOptions / WithHTTPCallOptions 后重新生成；按实际协议透传，原生客户端仍可直接使用 |
| [ ] 协议能力 | GRPCS 枚举、流式 RPC | GRPCS 已删除，不能假定旧枚举提供安全传输；当前 Factory gRPC 走 DialInsecure。TLS gRPC 与流式调用需业务独立适配，不属于生成适配器能力 |
| [ ] 数据库 | GetConnection、UseRead/UseWrite、replicas/datas 自动路由 | 改用 Connection、UseConnection；把目标库配置为独立连接；确认读流量没有全部迁回主库 |
| [ ] 数据库驱动 | 核心包隐式携带实现 | 显式空导入 `contrib/database/mysql` 或 `contrib/database/sqlite`；SQLite driver 名称为 `sqlite3` |
| [ ] 事务 | WithSqlTxOptions，可跨 Manager 误用事务 Context | 使用 `WithSQLTxOptions(*sql.TxOptions)`；事务绑定所属 Manager；嵌套事务不能重新指定 SQL 事务选项 |
| [ ] Redis | GetConnection 和 Manager 透传 Redis 方法 | 使用 `Connection(name)` / `Default()` 获取 client；Manager 持有 client，业务不得单独 Close |
| [ ] 任务 | job 配置与旧注册/Bootstrap | 使用 job.Spec、Manager 和 `bootstrap.NewJobBootstrap`；区分 Once/Daemon/Cron，业务任务响应 Context 取消 |
| [ ] 错误生成器 | IsXxx 同时检查 HTTP code | v2 使用 reason + reason_code；确认依赖 HTTP 状态区分错误的逻辑已迁移 |
| [ ] 错误消息 | 可变参数依赖旧格式化行为 | 单字符串原样保留；多个参数且首参数为 string 时格式化。建议先 Sprintf/Sprint 再传一个字符串 |
| [ ] 可选新能力 | 无统一 Queue/Kafka/OSS 接入 | 仅按业务需要接入；Queue 重试与死信不提供业务 exactly-once，仍需业务幂等 |

### 删除或改名的配置

已删除字段即使值是 null 或空对象也会被拒绝，不能留作占位。

| 旧字段 | 迁移方式 |
|---|---|
| 顶层 `log` | `LOG_*` 环境变量及 `pkg/log` 包级 `WithXXX` 设置 |
| 顶层 `metrics` | 显式构造 Provider，HTTP 指标端点使用 `server.http.metrics` |
| 顶层 `job`、`queue` | 强类型 Spec/构造配置与显式组装 |
| `app.disable_registrar` | 由 Wire provider 返回 `registry.Registrar`；返回 nil 禁用服务注册 |
| `server.middleware.timeout`、`client.clients.*.middleware.timeout` | 改成 `deadline`，按需求设置 fallback_timeout/max_timeout/min_budget |
| `database.connections.*.replicas/datas/trace_resolver_mode` | 删除，改为独立连接和显式选择 |
| `server.log`、`tracing.log`、`tracing.tracer_name` | Logger 派生和 Provider instrumentation scope |
| `redis.connections.*.read_only` | 删除，重新核对 Redis 接入方式 |
| `redis.connections.*.disable_indentity` | 拼写改为 `disable_identity` |

Deadline 缺失时默认回退超时为 10s，仅在父 Context 没有截止时间时生效；显式 `0s`
关闭回退超时。不要把旧 timeout 字段机械改名后沿用语义。

仅支持文档声明的热更新范围：app.stop_timeout、server.middleware、client.clients、
tracing.sampler 和数据库连接池参数。DSN、驱动、连接集合、服务监听地址等变化需要重启。
恢复 main 的 protobuf 字段编号后，此前未发布 v2 的二进制配置不能复用；从 YAML/JSON 重新生成。

### v2 开发期可选依赖迁移

此前 v2 开发版本的 `app.Spec.RegisterRegistrar`、`bootstrap.Spec.RegisterRegistrar` 和
`job.Spec.Coordinator`（包括 `job.Builder.Coordinator`）已移除。Registrar 改为
`app.NewApp` / `bootstrap.NewKratosApp` 的最后一个构造参数；Coordinator 改为
`job.NewManager` / `bootstrap.NewComponentsBootstrap` 的最后一个构造参数。
删除 Boot 中的对应登记，给 Wire 增加返回目标接口的 provider，再重新生成 injector。
禁用时返回 nil interface，启用时选择对应 contrib 实现；分布式 Cron 在协调器为 nil 时仍报构造错误。
完整示例和构造流程见 [Bootstrap 文档](pkg/bootstrap/README.md#可选依赖由-wire-构造注入)。

### CallOptions 迁移示例

优先使用 `client.WithGRPCCallOptions(ctx, grpc.WaitForReady(true))` 后调用重新生成的统一客户端。
切片隔离及 HTTP 对应 API 见 [client README](pkg/client/README.md#单次调用选项)。

需要直接使用原生客户端时，以下以业务生成的 `orderspb` 为例，业务持有完整调用租约：

```go
_, conn, release, err := factory.AcquireClient(ctx, "orders")
if err != nil {
    return nil, err
}
defer release()
if conn == nil {
    return nil, fmt.Errorf("orders requires a gRPC connection")
}
return orderspb.NewOrderServiceClient(conn).CreateOrder(ctx, request, grpc.WaitForReady(true))
```

调用方仍需给 ctx 设置合理截止时间。HTTP 同理，租用 HTTP client 后调用原生 HTTP
生成客户端；如果直接读取响应体，完成读取和 Body.Close 后才能归还租约。

### 生成器与版本

两个独立模块和二进制现在分别为：

- `github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-kratos-foundation-client-v2`
- `github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-kratos-foundation-errors-v2`

它们的 module 路径不在根模块 `/v2/cmd` 下。`-v2` 是工具名的一部分，不会让根模块的
`v2.x.y` tag 自动变成子模块版本。独立子模块发布需要自己的目录前缀 tag；按照当前 module
声明，例如 client 子模块的 `cmd/protoc-gen-kratos-foundation-client-v2/v1.0.0`，errors 同理。
这里只说明规则，不表示这些 tag 已存在；发布清单必须记录实际 tag 或固定提交。

业务构建应固定生成器版本，避免使用浮动 `@latest`。开发仓库可通过 `make init` 安装本地工具；
该 target 也会安装第三方生成工具。新 protoc 参数为：

```sh
protoc <业务原有参数> \
  --kratos-foundation-client-v2_out=paths=source_relative:. \
  --kratos-foundation-errors-v2_out=paths=source_relative:. \
  <业务协议文件>
```

显式 `--plugin` 映射的名称也必须带 `-v2`。重新生成后检查 import 指向框架 `/v2`、Wire 编译通过，
并删除不再使用的旧生成代码。原 HTTPImplProvider/GRPCClientImplProvider 别名已删除，改用 XxxProvider。

## 业务验收与自动化入口

```sh
make init-lint       # 首次准备 lint 工具，版本由 Makefile 固定
make lint            # 根模块和三个生成器模块使用同一份 .golangci.yml
make test-business   # 下面的业务矩阵，强制重新执行
make verify-release  # 顺序执行 verify、lint、test-business，失败立即返回非零
```

`verify-release` 不发布、不推送、不打 tag，也不修改生成文件。`verify` 包含逐模块 test/vet/race；
覆盖门禁只保证手写函数不是 0% 执行覆盖，不代表分支全覆盖或断言完整。

`test-business` 使用现有 Wire 外部模块 fixture：临时生成 Wire 代码，使用当前工作树的公开 API，
启动真实 App 和 HTTP 服务，通过真实 SQLite 驱动提交及回滚订单。每次使用独立临时数据库和
本机随机端口，追踪导出关闭，不依赖 MySQL、Redis、Consul 或云账号。Wire 子进程测试启用
`-race -count=1`；生成客户端的契约测试另行在临时模块编译执行。

| 自动验收项 | 成功条件 | 测试位置 |
|---|---|---|
| Wire 构造与冻结 | 基础设施/业务贡献完成后才冻结 Spec | `pkg/bootstrap/wire_integration_test.go` |
| HTTP 订单提交 | 返回 201，默认库只有成功订单 | fixture `TestBusinessHTTPTransactionsAndCleanup` |
| SQL 已写入后业务拒绝 | 返回 422，被拒绝订单未持久化 | 同上 rollback 子用例 |
| 显式连接选择 | audit 订单只出现在 audit 数据库 | 同上 named connection 子用例 |
| 错误输入和未知连接 | 返回 400/500，不写入订单、不暴露原始数据库错误 | 同上 invalid input/unknown connection 子用例 |
| App 与资源所有权 | 父 Context 取消后 Run 退出；数据库直到 Wire cleanup 才关闭，重复 cleanup 安全 | 同上 |
| 构造失败回滚 | NewApp 失败后恢复全局 Logger | fixture `TestGeneratedAssemblyRollsBackOnAppConstructionFailure` |
| 旧配置拒绝 | log/metrics/job/timeout/replicas 返回 ErrRemovedField | fixture `TestBusinessRejectsRemovedConfig` |
| 生成客户端契约 | HTTP/gRPC 分支、错误链和租约释放通过 | client-v2 `TestGeneratedClientsCompileAndReleaseLeasesAgainstPublicFactory` |

HTTP fixture 是最小订单场景，不代替真实业务服务的权限、限流、幂等及数据迁移验收。
生成客户端契约测试替代了协议网络边界，不代表真实 gRPC 服务互通；项目的重连测试另由 `make verify` 执行。

```mermaid
flowchart TD
    A([开始业务验收]) --> B[临时文本配置和 SQLite 文件]
    B --> C[Wire 生成公开构造图]
    C --> D{配置和构造是否成功?}
    D -- 否 --> E[检查旧字段错误或构造回滚]
    E --> Z([测试返回结果])
    D -- 是 --> F[App.Run 启动 HTTP 随机端口]
    F --> G[HTTP 请求进入订单事务]
    G --> H[SQL 写入指定连接]
    H --> I{业务是否接受?}
    I -- 是 --> J[提交并返回 201]
    I -- 否 --> K[回滚并返回 422]
    G -- 输入错误或未知连接 --> L[返回 400 或 500]
    J --> M[检查两个数据库最终记录]
    K --> M
    L --> M
    M --> N[取消父 Context 并等待 Run 退出]
    N -- 等待超时 --> O[测试失败: application did not stop]
    N -- 已退出 --> P[Wire cleanup 逆序释放资源]
    P --> Q[检查关闭状态和幂等 cleanup]
    Q --> Z
    O --> Z
```

图中 App.Run 是测试拥有的唯一新增执行协程，主测试通过父 Context 取消它，并通过 done channel
等待退出后才释放资源；未新增业务锁或改变框架同步策略。测试失败由 Go testing 报告，未虚构业务日志事件。

## 发布前人工清单

- [ ] 完成上面的迁移项；对未使用的能力标明“不适用”，不要默认为已验证。
- [ ] 运行 `make proto`，检查生成产物与预期一致；稳定提交上再次生成应无差异。
- [ ] 执行 `make verify-release`，保留提交 SHA、Go/lint/protoc 版本及命令输出。
- [ ] 从业务仓库重新生成 Wire 和全部协议，完成 HTTP/gRPC 真实互通、CallOptions 透传验收。
- [ ] 使用隔离测试环境验证实际采用的 MySQL/Redis/Consul/Kafka/OSS；检查断连恢复、权限、数据隔离。
- [ ] 若使用队列，验证重复投递、幂等、死信和停机期间确认语义。
- [ ] 若原来使用读副本/表路由，确认显式连接选择及主从负载分布。
- [ ] 演练配置非法更新、订阅终止告警和需要重启的配置变更。
- [ ] 演练滚动发布与回退；旧二进制应使用其对应旧配置，不能直接读取迁移后的 v2 配置。
- [ ] 在业务负载下验证延迟、连接池容量和整体停机时长；本地测试不提供性能结论。
- [ ] 记录根模块与各生成器的实际版本；经发布流程批准后再打 tag、推送或部署。

```mermaid
flowchart LR
    A([固定发布提交]) --> B[迁移清单和代码生成检查]
    B --> C[make verify-release]
    C --> D{本地门禁通过?}
    D -- 否 --> E[修复并重新验证]
    E --> B
    D -- 是 --> F[隔离环境业务验证与回退演练]
    F --> G{验收通过?}
    G -- 否 --> E
    G -- 是 --> H([进入既有发布审批流程])
```

默认 HTTP 现在保留 `/healthz`、`/readyz`，不受业务前缀和鉴权影响；已有同名路由需迁移或显式关闭健康端点。详见 [server README](pkg/server/README.md#默认健康检查)。配置状态及指标接入见 [config README](pkg/config/README.md#运行状态观测与过载处置)。

监控端点地址可通过 `server.http.metrics.addr` 和 `server.http.health.addr` 指定；为空时复用业务 HTTP，相同时共享监听。路径相对于所选监听根路径，不再受业务 PathPrefix/Filter 影响，已有前缀抓取地址需同步调整。独立端口需同步更新部署端口与抓取/探针地址；手工组装需额外登记 `Runtime.ManagementServers()`，ServerBootstrap 自动登记。

## 移除配置自身引用

配置只保留环境变量模板 `$VAR` / `${VAR}`，在 Source 返回原始 KeyValue 后、JSON/YAML 格式解析前执行一次替换；不再解析配置字段之间的引用。旧 `${path}` 不再读取同名配置字段；若名称合法但环境变量未设置，则替换为空。包含点分路径等非法变量语法会报模板错误。`${path:fallback}` 不再合法，需要默认值时改用环境变量 `${VAR:-fallback}`，必填检查使用 `${VAR:?描述}`。配置引用的循环检查同步移除。

迁移时将配置引用改为实际值、部署时生成的配置或环境变量。数字、布尔值环境模板不加引号，字符串按 JSON/YAML 规则加引号和转义。采用 compose-go/template v2.15.0：普通未设置变量和空变量都替换为空；字面 `$` 使用 `$$` 转义；替换后的内容仍须满足格式及目标类型约束。详细边界见 [环境变量模板](pkg/config/README.md#环境变量模板)。

```mermaid
flowchart TD
    A([配置源返回原始 KeyValue]) --> B[Compose 环境替换 默认值与必填检查]
    B -- 模板或必填检查失败 --> I
    B --> E{解析 JSON/YAML 成功?}
    E -- 是 --> F[按优先级合并 不展开配置自身引用]
    F --> G{保留字段校验通过?}
    G -- 是 --> H([发布快照 供 Load 和订阅解码])
    E -- 否 --> I{热更新?}
    G -- 否 --> I
    I -- 是 --> J[ERROR manager.watch config.rejected 通知订阅 保留旧快照]
    I -- 否 --> K[释放源 返回构造错误]
    J --> L([等待下一次源更新])
    K --> M([结束])
```

## 消费项目迁移经验：auth_service

以下经验来自 2026-09-11 对 auth_service 工作区的审阅（业务基线 `7930c17`，
Foundation 基线 `7de373d`，两者均含未提交改动）。这是开发期案例，不是发布验收记录；
`require v2.0.0` 配合本地 `replace` 只能说明声明版本，不能证明正在运行已发布的 v2.0.0。

### 先确认实际依赖，再整理迁移改动

需要分别记录四种来源：Foundation 运行库、生成器、配置 Schema、公共构建脚本。
本例运行库由 `go.mod replace` 选择；Schema 由解析出的模块目录提供；三个生成器是独立模块，
已安装二进制不会随运行库更新；公共 Makefile 又来自 `CYBERKITE_DIR` 指定的另一份工作区。
只固定业务仓库提交，仍可能无法在 CI 重现生成结果。

在消费项目根目录检查运行库和工具，工具路径按项目实际位置调整：

```sh
go list -m -f '{{.Path}} {{.Version}} {{.Dir}}' github.com/jaggerzhuang1994/kratos-foundation/v2
go version -m ./tools/protoc-gen-kratos-foundation-client-v2
go version -m ./tools/protoc-gen-kratos-foundation-errors-v2
go version -m ./tools/protoc-gen-jsonschema
```

本例的 `FOUNDATION_PLUGIN_DIR`、`FOUNDATION_PLUGIN_VERSION`、`FOUNDATION_CONFIG_SCHEMA`
属于消费项目公共 Makefile 的变量，不是 Foundation 的统一配置 API。核对项目默认赋值后再选择来源：
若项目已写入 `FOUNDATION_PLUGIN_DIR ?= /本地路径`，仅 `unset FOUNDATION_PLUGIN_DIR` 不会切回远端；
应删除该默认值或在命令行显式传 `FOUNDATION_PLUGIN_DIR=`。Make 变量值不要自带 shell 引号，
由使用变量的 recipe 负责引用路径。

本地 Foundation 增加依赖后，消费项目也要重新整理依赖。本次原工作区定向测试在编译前报告
`compose-go/v2/template` 缺少 `go.sum` 条目；应在消费项目执行 `go mod tidy` 并审阅差异，
不要按报错提示直接依赖 Foundation 的 `internal` 包，也不要手改 `go.sum`。
发布前移除绝对路径 replace，固定运行库、各生成器和公共脚本的实际版本，再在干净 checkout 验证。

### Wire 成功不代表业务服务已登记

本例已采用 `ConsulBaseProviderSet`、单一 `bootstrap.Spec` 和统一组件构造，
但审阅时 `cmd/auth_service/bootstrap.go` 的 Auth HTTP、Auth gRPC、RBAC gRPC 注册调用均被注释，
对应服务参数也被移除。`wire_gen.go` 因此没有构造 AuthService、RbacService 及其业务依赖。
ProviderSet 列出了构造函数，并不表示 Wire 一定执行它们。

迁移检查应同时覆盖以下三个层次：

- 编译图：业务 Bootstrap 显式接收实际服务，生成的 Wire 包含所需业务和资源构造及 cleanup。
- 注册图：HTTP/gRPC 注册回调实际调用生成的 `RegisterXxx`；空回调只能证明组件被选择。
- 运行契约：用真实业务 HTTP 路由和 gRPC 方法请求验证成功与失败响应，不能只检查监听端口或健康探针。

恢复业务登记应修改 Bootstrap/Wire 源并重新生成，不直接修改 `wire_gen.go`。
演示 Job、调试生命周期输出和日志全局设置应单独核对是否属于应用需求，不能作为迁移模板照搬。
Registrar 与 Job Coordinator 由 Wire 构造注入，细节见 [Bootstrap 文档](pkg/bootstrap/README.md)。

### 协议搬迁要统一映射，并控制类型来源

本例把 Auth/RBAC 和调用方所需的 Base API 移到业务 `api/`，使用模块内生成包；
共享 `cyberkite` 类型仍引用原依赖包，`third_party/cyberkite` 仅作 include。
不要把“API 已搬迁”理解为“全部共享协议也要在每个服务重新生成”。
搬迁前后核对 protobuf package、消息/枚举全名、字段编号、RPC 名称及 HTTP 绑定；
Go import 改变不应顺带改变线上协议。若同一进程仍经间接依赖导入旧 API 包，要检查是否重复注册同名描述符。

统一 `M文件=Go包路径` 映射应传给所有启用的插件，而不仅是 Go 插件：
Go、gRPC、HTTP、Foundation client/errors、校验和文档产物必须指向一致的类型。
本例 PGV 的生成目标不能仅靠 M 参数重定位，公共脚本使用适配器在内存
`CodeGeneratorRequest` 中设置相同的 `go_package`，再调用原 PGV；不修改源 proto 或手改生成文件。
这是该工具链的处理方式，不能假定所有插件都遵守同一参数语义。

还应拒绝不同 include 根下的同名 proto import 路径，保留第三方协议自身的 `go_package`，
并使 include 副本与实际 Go 依赖版本同步。生成后检查输出目录、package/import、预期文件是否存在，
在稳定输入和固定工具上再次生成应无差异。仅检查 protoc 退出码会漏掉错误目录或缺少产物的问题。

### 配置验收覆盖来源、合并结果和外部配置

目录转 `file.PathList` 的规则由业务定义。本例是根目录 `*.yaml` 后加载 `{APP_ENV}/*.yaml`，
每组按 glob 字典序展开；不递归，也不包含 `.yml`。单文件、空路径、缺失路径和空目录都有独立语义。
示例放在独立子目录可避免被默认 glob 当成实际配置加载。

`ConsulBaseProviderSet` 的 `NewConsulSources` 在 local 环境让文件优先，其他环境让 Consul 优先；
同组内后面的源覆盖前面的源。Consul KV 前缀和环境路径仍由业务提供，
并且 local 文件优先不等于禁用 Consul 或远端故障回退。

本例 `internal/conf/source_test.go` 用临时文件验证顺序和最终覆盖值，并通过 v2 Manager
读取业务 Duration 与 `server.middleware.deadline.fallback_timeout`。这类测试比单独检查 YAML 语法更有效，
但仍不等于构造全部组件或验证 Consul 中的实际配置。发布前还应逐份清理远端旧字段，
检查环境模板替换后的值，并用隔离环境验证组件构造。业务 Schema 应合并所选运行库的
`config.schema.json`，避免编辑器接受运行时已经拒绝的字段。

### 错误迁移必须经过实际传输边界

WebAuthn 撤销凭证错误携带 `rpId` 和 `credentialId`，调用方依赖这些字段执行后续动作。
只验证业务函数返回的 `WithHTTPData`，无法证明 HTTP 网关经过 gRPC 后仍能读取它。
本例 `TestNewWebAuthnCredentialRevokedError` 验证：

```text
业务错误 → FromError → GRPCStatus().Err() → FromError → HTTPData()
```

断言包括 HTTP code、reason_code、生成的 `IsXxx`、JSON 字段名和 base64url 凭证 ID。
当前 Foundation 的 [gRPC 错误实现](pkg/errors/grpc.go) 已携带并恢复可 JSON 编码的 HTTPData，
相关契约位于 [gRPC 错误测试](pkg/errors/grpc_test.go)；发布版本需要包含该行为。
此单测覆盖 status 编解码，不代表真实网络和网关链路已经通过；还需实际 gRPC→HTTP 互通验收。
不要要求内部错误栈、cause 或响应头同样跨网络保留。

Redis 迁移除 `Manager.Default()` / `Connection(name)` 外，还应将缺失键判断迁到
`github.com/redis/go-redis/v9` 的 `Nil`，继续使用 `errors.Is`。
本例缺失会话映射“已过期”，基础设施失败映射内部错误，两者不能在替换接口时合并。

### 建议的验收顺序

```mermaid
flowchart TD
    A([开始]) --> B[记录运行库 工具 Schema 公共脚本来源]
    B --> C[整理消费项目依赖 生成协议与 Wire]
    C --> D{依赖 编译 产物检查通过?}
    D -- 否 --> E[记录命令错误 修正来源或生成配置]
    E --> B
    D -- 是 --> F{Wire 包含业务构造且回调登记实际服务?}
    F -- 否 --> G[恢复业务依赖和登记 重新生成]
    G --> C
    F -- 是 --> H[验证文件覆盖 错误传输和业务单测]
    H --> I[隔离环境连接 Consul MySQL Redis]
    I --> J[请求实际 HTTP 与 gRPC 业务接口 验证超时和停机]
    J --> K{业务契约和 cleanup 通过?}
    K -- 否 --> L[记录测试失败及实际应用日志 定位迁移缺口]
    L --> C
    K -- 是 --> M[固定发布版本 在干净 checkout 复验]
    M --> N([记录已验证项和剩余限制])
```

图中记录节点是验收动作，不表示框架新增日志或自动回退。
本例暴露的未完成项包括业务注册被注释、消费项目依赖清单落后于本地 Foundation、
运行库和工具来源含本机路径，以及 README 对当前协议目录、插件来源和服务登记的描述漂移。
应以当前源码和生成图为准，把这些项关闭后再宣告迁移完成。
