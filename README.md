# kratos-foundation

基于 [go-kratos v2](https://github.com/go-kratos/kratos) 的 Go 服务基础框架。业务只写业务代码，由框架统一提供应用生命周期、配置、日志、HTTP/gRPC 服务与客户端、数据库、缓存、消息与任务、可观测性等能力，并通过 Wire 在编译期完成组装。

适用于 HTTP 服务、gRPC 服务、API 网关、队列消费者、定时任务、一次性脚本和自定义常驻进程。

- 模块：`github.com/jaggerzhuang1994/kratos-foundation/v2`
- 要求：Go 1.26+；构建与发布使用已修复标准库漏洞的工具链（当前 go1.26.10+）
- 版本：以 Git tag 为准，变更与升级风险见 [CHANGELOG](CHANGELOG.md)，从 v1 迁移见 [MIGRATION](MIGRATION.md)

## 能力一览

| 领域 | 包 | 提供的能力 |
| --- | --- | --- |
| 应用 | [`app`](pkg/app/README.md)、[`bootstrap`](pkg/bootstrap/README.md) | 统一启动/停止、信号与停机预算、启动失败回滚、Runtime panic 防护、服务注册；`BaseProviderSet` 一次装配全部基础组件 |
| 配置 | [`config`](pkg/config/README.md)、[`contrib/config`](contrib/config) | 文件 / Consul / 文本源合并、环境变量模板、强类型解码、按 key 订阅热更新 |
| 日志 | [`log`](pkg/log/README.md) | 结构化日志、模块级别策略、文件轮转、请求级 debug、caller 定位；生产环境默认脱敏诊断字段 |
| 服务端 | [`server`](pkg/server/README.md) | HTTP/gRPC/WebSocket、默认中间件链（错误归一化、限流、超时、校验、访问日志）、`/healthz` `/readyz` `/metrics`、gRPC 健康检查与就绪联动、平滑停机 |
| 客户端 | [`client`](pkg/client/README.md)、[`registry`](pkg/registry/README.md) | 具名 HTTP/gRPC 客户端、服务发现、超时预算传播、热更新与租约式借用；Consul 注册发现 |
| 错误 | [`errors`](pkg/errors/README.md) | 带业务码的结构化错误，HTTP/gRPC 双向转换，兼容 v1 错误；错误堆栈仅在非生产环境返回 |
| 数据 | [`database`](pkg/database/README.md)、[`redis`](pkg/redis/README.md)、[`oss`](pkg/oss/README.md)、[`lock`](pkg/lock/README.md) | GORM 具名连接与事务、连接池热更新、Redis 连接管理、对象存储、分布式锁 |
| 消息与任务 | [`kafka`](pkg/kafka/README.md)、[`queue`](pkg/queue/README.md)、[`job`](pkg/job/README.md) | Kafka 生产消费与重试/死信、持久化任务队列（Redis / 数据库）、Cron 定时任务与并发策略 |
| 可观测性 | [`metrics`](pkg/metrics/README.md)、[`tracing`](pkg/tracing/README.md)、[`observability`](observability/README.md) | OpenTelemetry 指标与链路、Prometheus 暴露、`app_ready` 就绪指标、Grafana 面板与告警规则 |
| 工具 | [`env`](pkg/env/README.md)、[`validation`](pkg/validation/README.md)、[`crypto`](pkg/crypto/README.md)、[`compress`](pkg/compress/readme.md)、[`totp`](pkg/totp/README.md)、[`utils`](pkg/utils/README.md)、[`gormscope`](pkg/gormscope/README.md) | 运行环境、输入校验、加解密、压缩、一次性验证码、集合工具、GORM 查询条件 |

`contrib/` 提供可选的第三方实现（MySQL、SQLite、Consul、阿里云 OSS、Redis 锁、队列存储），按需空导入。完整包分类见 [pkg/README.md](pkg/README.md)。

## 快速开始

### 1. 组装应用

业务提供应用身份、配置路径和自己的 ProviderSet，其余由框架组装：

```go
//go:build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/bootstrap/consulconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

func wireApp(appinfo.AppInfo, bootstrap.LocalConfigPaths) (*app.App, func(), error) {
	panic(wire.Build(
		bootstrap.BaseProviderSet, // 配置、日志、指标、链路、服务端、任务、注册发现与应用生命周期
		consulconfig.ProviderSet,  // local 环境读本地文件，其他环境读 Consul KV
		// 每个前缀下按顺序加载；路径支持 {{app}}、{{env}}、{{version}} 模板。
		wire.Value(consulconfig.ConsulConfigPrefix{"configs", "secrets"}),
		wire.Value(consulconfig.ConsulConfigPaths{"common.yaml", "{{app}}/*.yaml", "{{app}}/{{env}}/*.yaml"}),
		internal.ProviderSet, // 业务自己的 data/biz/service 构造函数
		Bootstrap,
	))
}
```

```go
func main() {
	application, cleanup, err := wireApp(appinfo.New(Version), bootstrap.LocalConfigPaths{"./configs"})
	if err != nil {
		panic(err)
	}
	defer cleanup()
	if err := application.Run(); err != nil { // 阻塞到收到信号或运行时失败
		panic(err)
	}
}
```

### 2. 声明服务

`Bootstrap` 在构造期向共享的 `bootstrap.Spec` 登记服务、健康检查、任务和自定义运行时；启动与停止由应用统一负责：

```go
func Bootstrap(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, svc *service.OrderService) (bootstrap.Bootstrap, error) {
	spec.Http().Register(func(srv server.HTTPServer) error {
		orderpb.RegisterOrderServiceHTTPServer(srv, svc)
		return nil
	})
	spec.Grpc().Register(func(srv server.GRPCServer) error {
		orderpb.RegisterOrderServiceServer(srv, svc)
		return nil
	})
	// 依赖检查失败时 /readyz 返回 503，gRPC 健康检查返回 NOT_SERVING。
	spec.Health().Checks(server.ReadinessCheck{Name: "db", Check: svc.Ping})
	return bootstrap.Bootstrap{}, nil
}
```

### 3. 配置

```yaml
app:
  stop_timeout: 30s
server:
  stop_delay: 3s
  http:
    addr: 0.0.0.0:8000
  grpc:
    addr: 0.0.0.0:9000
database:
  connections:
    default:
      driver: mysql
      dsn: ${MYSQL_DSN}
```

完整字段见 [config.example.yaml](config.example.yaml)，编辑器补全与校验使用 [config.schema.json](config.schema.json)。运行环境由 `APP_ENV`（local/dev/test/pre/prod）决定，见 [pkg/env](pkg/env/README.md)。

可运行的完整示例见 [examples/minimal](examples/minimal/README.md) 与 [examples/components](examples/components/README.md)。

## 常用组件

### 数据库与事务

```go
import _ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/mysql" // 选择编译进应用的驱动

func (r *orderRepo) Pay(ctx context.Context, id string) error {
	return r.db.Transaction(ctx, func(ctx context.Context) error {
		// 回调内用同一个 ctx 取连接，自动复用当前事务
		return r.db.Connection(ctx).Model(&Order{}).Where("id = ?", id).Update("status", "paid").Error
	})
}
```

### Redis

```go
func newCache(manager redis.Manager) *cache { return &cache{client: manager.Default()} } // 借用共享连接，不要 Close
```

### 调用其他服务

客户端由 `client.Factory` 按服务名创建并共享；每次调用借用并归还，配置热更新后旧连接在归还后释放。业务通常使用代码生成器 [protoc-gen-kratos-foundation-client-v2](cmd/protoc-gen-kratos-foundation-client-v2/README.md) 生成的封装：

```go
conn, _, release, err := factory.AcquireClient(ctx, "user_service")
if err != nil {
	return err
}
defer release()
```

### 定时任务

```go
spec.Job().RegisterCron("cleanup", "@every 1m", job.TaskFunc(func(ctx context.Context) error {
	return svc.Cleanup(ctx)
}))
```

调度表达式、启停和并发策略可通过 `job.cron.<name>` 热更新，见 [pkg/job](pkg/job/README.md)。

### 消息与队列

- Kafka 消费：构造 `kafka.ConsumerRuntime` 后 `spec.RegisterKafkaConsumer(consumer)`，提交、重试与死信见 [pkg/kafka](pkg/kafka/README.md)。
- 持久化任务：`queue.Queue[T]` 投递，`queue.Worker[T]` 消费并 `spec.RegisterRuntime(worker)`，存储使用 [Redis](contrib/queue/redis/README.md) 或[数据库](contrib/queue/database/README.md)，见 [pkg/queue](pkg/queue/README.md)。
- 自定义常驻进程：实现 `Start(ctx) error` / `Stop(ctx) error` 后 `spec.RegisterRuntime(r)`。

### 配置热更新

```go
value, cleanup, err := config.NewHotReloadValue[conf.Biz](manager, "biz")
if err != nil {
	return nil, nil, err
}
current, _ := value.GetCurrent() // 共享只读快照，非法更新会保留旧值
```

### 日志与错误

```go
logger := log.WithModule("order") // 或注入 log.Logger 后 logger.WithModule("order")
logger.WithContext(ctx).With("event", "order.payment.failed", "order.id", id, "error", err).Error("payment failed")

return errors.New(http.StatusConflict, "ORDER_PAID", "order already paid").WithCause(err)
```

日志事件统一为 `<领域>.<对象>.<动作>`，字段约定见 [pkg/log](pkg/log/README.md)。错误的 `err_stack` 只在 local/dev/test（pre 需 `APP_DEBUG=true`）返回给调用方，生产环境只写服务端日志。

## 部署与监控

- 健康检查：`/healthz` 表示进程存活，`/readyz` 表示应用已启动完成、依赖检查通过且未在停机；gRPC 健康检查空服务名对应就绪，`liveness` 对应存活。可通过 `server.http.health.addr` 独立管理端口。
- 平滑停机：收到 SIGTERM 后先撤销就绪，等待 `server.stop_delay` 让流量摘除，再在 `app.stop_timeout` 预算内停止各运行时。Kubernetes 的 `terminationGracePeriodSeconds` 应大于 `stop_timeout`。
- 监控：[observability](observability/README.md) 提供 Grafana 面板、Prometheus 告警与 recording rules 及接入说明。
- TLS/mTLS 由网关或 Service Mesh 终止，框架监听默认明文，只能部署在受信任网络。

## 开发

```bash
make init      # 安装固定版本的开发工具到 tools/
make verify    # 单元测试 + vet + race，不依赖外部服务
make lint
make help      # 查看全部命令
```

测试与本地环境说明见 [test/README.md](test/README.md)，贡献规范见 [AGENTS.md](AGENTS.md)，新增包的设计模式见 [pkg/DEVELOPMENT.md](pkg/DEVELOPMENT.md)。
