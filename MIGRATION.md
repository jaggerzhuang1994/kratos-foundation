# 迁移指南

- **v1（`github.com/jaggerzhuang1994/kratos-foundation`）→ v2**：按本文操作。
- **v2.x 升级到更新的 v2**：逐版本阅读 [CHANGELOG](CHANGELOG.md) 的“升级风险”，通常只需改动其中列出的项目。

迁移只要求**接入新框架**，不要求重构业务层（biz/data/service 的拆分、命名或实现）。把框架迁移和业务重构放在不同的提交里，便于回归。

## 迁移步骤

1. **工具链与模块**：Go 1.26+；import 前缀改为 `github.com/jaggerzhuang1994/kratos-foundation/v2`，执行 `go mod tidy`。
2. **Wire 入口**：injector 返回 `*app.App`，provider 使用 `bootstrap.BaseProviderSet` + `consulconfig.ProviderSet`（或自定义配置源），业务入口函数签名为 `func Bootstrap(bootstrap.InfrastructureBootstrap, *bootstrap.Spec, ...) (bootstrap.Bootstrap, error)`。删除旧的领域 Bootstrap、`ApplicationSpec` 等 provider，然后 `go tool wire ./cmd/...`。参考 [README 快速开始](README.md#快速开始)。
3. **服务注册**：在 `Bootstrap` 中用 `spec.Http().Register`、`spec.Grpc().Register`、`spec.Health().Checks`、`spec.Job().RegisterCron`、`spec.RegisterRuntime` 登记；不要再自行 `kratos.New`。
4. **驱动空导入**：`_ "…/v2/contrib/database/mysql"`、`_ "…/v2/contrib/registry/consul"`、`_ "…/v2/contrib/oss/aliyun"` 按需导入。
5. **配置**：按下方速查表清理已删除或改名的字段（旧字段会被**静默忽略**，不会报错）；`server.http.disable` / `server.grpc.disable` 控制监听。
6. **调用替换**：按速查表替换数据库、Redis、客户端、日志与应用身份 API。
7. **协议代码**：用 `protoc-gen-kratos-foundation-client-v2`、`protoc-gen-kratos-foundation-errors-v2` 重新生成客户端与错误代码，删除旧生成文件。
8. **部署**：探针改为 `/healthz`（存活）与 `/readyz`（就绪）；设置 `server.stop_delay`、`app.stop_timeout`，并确保 `terminationGracePeriodSeconds` 大于 `stop_timeout`。

## 速查表

| v1 | v2 |
| --- | --- |
| `pkg/app_info`、`GetId/GetName` | `pkg/appinfo` 的 `ID/Name/Version/Metadata` |
| Kratos `config.Value/Watch` | `config.Manager.Load/Subscribe`，热更新用 `config.NewHotReloadValue` |
| `ResolveClient` / `MakeGrpcConn` / `MakeHttpClient` | `factory.AcquireClient(ctx, name)`，成功后必须 `defer release()` |
| `WithDefaultConnName` / `WithConnName` | 生成客户端 `NewXxxWithConnName(factory, name)` |
| `GrpcCallOptionFromContext` 等 | `client.WithGRPCCallOptions` / `client.WithHTTPCallOptions` |
| `GetConnection`、`UseRead/UseWrite`、`replicas` 自动读写分离 | `Connection(ctx)`、`UseConnection`；读写分离改用 `read_replicas` 与 `database.UsePrimary`，见 [只读副本](pkg/database/README.md#只读副本) |
| `WithSqlTxOptions` | `database.WithSQLTxOptions` |
| Redis `GetConnection`、Manager 透传命令 | `Connection(name)` / `Default()` 获取 client，由 Manager 关闭 |
| `HTTP().HealthChecks(...)` | `spec.Health().Checks(...)`，地址与路径在 `server.http.health` |
| `HTTPBuilder/GRPCBuilder.Enable/Disable` | 配置 `server.http.disable` / `server.grpc.disable` |
| `RunImmediately()` | `RunImmediately(true)` |
| 错误生成器 `IsXxx` 依赖 HTTP code | 依据 `reason` + `reason_code` 判断 |
| `server.middleware.*`、`client.clients.*.middleware.*` | 去掉 `middleware` 层，迁移到 `server.*`、`client.clients.*.*` |
| `server.middleware.timeout` | `server.deadline`（`fallback_timeout` / `max_timeout` / `min_budget`），语义不同，勿机械改名 |
| 顶层 `metrics`、`queue`、旧 `job` 字段 | 显式构造；指标端点用 `server.http.metrics`，定时任务用 `job.cron` |
| `app.disable_registrar` | `registry.instances.<name>.driver: "null"` |
| `redis.connections.*.disable_indentity` | `disable_identity` |
| `database.connections.*.replicas/datas/trace_resolver_mode`、`redis.*.read_only` | 删除 |

框架与业务的 v1 服务可以混合调用：`x-md-*` metadata、W3C trace、HTTP JSON 错误与 gRPC `ErrorInfo` 双向兼容。v1 HTTP 不传剩余超时预算，只能依赖 v2 本地 deadline。

## 验收

- `go build ./...`、`go vet ./...`、现有测试通过，`go tool wire` 无差异。
- 本地启动后 `/healthz`、`/readyz` 返回 200，`/metrics` 有 `app_ready 1`。
- 每个 HTTP/gRPC 接口至少调用一次，核对响应体与错误码（`code`、`reason`、`message`）与迁移前一致。
- 队列、Kafka、定时任务在测试环境各触发一次；停机时日志出现 `app.stopping` 与 `app.stopped`，无强制中断。
- 日志平台中按旧事件名或旧字段建的查询与告警已同步（对照 CHANGELOG）。

## AI 迁移提示词

```text
把当前项目从 kratos-foundation v1 迁移到 github.com/jaggerzhuang1994/kratos-foundation/v2（版本：<tag 或 commit>）。

要求：
1. 只做接入框架所需的修改，不重构业务层（biz/data/service 的结构、命名与业务逻辑保持不变）。
2. 先阅读框架仓库的 README.md、MIGRATION.md 与 CHANGELOG.md 的“升级风险”，再阅读本项目的 Makefile、go.mod、cmd/*/wire.go 与 configs/。
3. 按 MIGRATION.md 的“迁移步骤”逐项修改；生成文件（wire_gen.go、*.pb.go）只通过生成命令更新，禁止手改。
4. 对照“速查表”清理配置中已删除或改名的字段，列出被删除的字段及原因。
5. 每完成一步运行 go build ./... 与 go vet ./...；最后运行项目测试并按“验收”逐项说明结果。
6. 遇到速查表未覆盖、或需要改变业务行为的地方，停止并说明选项，不要自行决定。
7. 交付时给出：修改的文件清单、配置差异、未验证项（例如需要真实环境的队列或 Kafka）。
```
