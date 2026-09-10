# main → v2 迁移与发布清单

本文对应 main `4fd91b2` 到 v2 的公共接口迁移。适用于应用接入方和框架发布者；
迁移完成意味着应用完成配置和 Wire 改造、重新生成协议代码，并通过本文的业务验收。
仅改 import 前缀不构成迁移完成。

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
| `app.disable_registrar` | 在组装层决定是否登记 Registrar |
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
