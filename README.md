# Kratos Foundation

[English](#english) | [中文](#中文)

---

<a name="中文"></a>

基于 [Kratos](https://github.com/go-kratos/kratos) 微服务框架的生产级基础设施库，提供开箱即用的企业级微服务开发能力。

## ✨ 核心特性

### 🏗️ 完整的微服务基础设施
- **服务发现与注册** - 集成 Consul，支持服务自动注册、发现和健康检查
- **分布式追踪** - 基于 OpenTelemetry，支持 OTLP HTTP 导出器
- **指标采集** - Prometheus 指标，自动埋点 HTTP/gRPC/数据库/缓存
- **结构化日志** - 支持日志过滤、轮转、多输出目标，自动注入 TraceID
- **依赖注入** - 基于 Google Wire 的编译时 DI

### 💾 数据层
- **数据库访问** - GORM 集成，支持多数据库（MySQL/PostgreSQL/SQLite）、读写分离、链路追踪
- **Redis 缓存** - 多连接池管理，集成追踪与指标

### 🌐 服务层
- **双协议服务** - HTTP 与 gRPC 服务器，支持 WebSocket
- **客户端工厂** - 服务发现与直连模式，支持熔断、重试、超时策略
- **中间件系统** - 统一的服务端/客户端中间件（日志、追踪、限流、熔断等）

### ⏰ 任务调度
- **定时任务** - Cron 调度，支持并发策略控制（SKIP/OVERLAP/DELAY）

### 🛠️ 开发工具
- **protoc-gen-jsonschema** - Protocol Buffer 转 JSON Schema（支持 Draft-04/06/07/2019-09/2020-12）
- **protoc-gen-kratos-foundation-errors** - 错误码生成器
- **protoc-gen-kratos-foundation-client** - 客户端代码生成器

## 📋 环境要求

- Go >= 1.23
- Protocol Buffers 编译器 (protoc)

## 🚀 快速开始

### 安装

```bash
go get github.com/jaggerzhuang1994/kratos-foundation
```

### 安装开发工具

```bash
make init
```

这将安装以下工具：
- `wire` - 依赖注入代码生成
- `protoc-gen-go` / `protoc-gen-go-grpc` - Protocol Buffer 代码生成
- `protoc-gen-go-http` - Kratos HTTP 代码生成
- `protoc-gen-kratos-foundation-errors` - 错误码生成
- `protoc-gen-kratos-foundation-client` - 客户端生成
- `protoc-gen-jsonschema` - JSON Schema 生成
- `protoc-gen-validate` - 参数校验生成
- `protoc-gen-openapiv2` - OpenAPI 文档生成
- `golangci-lint` - 代码检查工具

### 项目初始化

#### 1. 使用 Wire 组装依赖

创建 `wire.go`：

```go
//go:build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/app"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/client"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/database"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
)

// 组合所有基础 ProviderSet
var infraProviderSet = wire.NewSet(
	log.ProviderSet,      // 日志
	consul.ProviderSet,   // Consul 客户端
	registry.ProviderSet, // 服务注册
	tracing.ProviderSet,  // 分布式追踪
	metrics.ProviderSet,  // 指标采集
	database.ProviderSet, // 数据库
	redis.ProviderSet,    // Redis
	client.ProviderSet,   // 客户端工厂
	server.ProviderSet,   // 服务器
	app.ProviderSet,      // 应用
)

func wireApp() (*kratos.App, func(), error) {
	panic(wire.Build(
		infraProviderSet,
		// 添加你的业务 ProviderSet...
		// service.ProviderSet,
		// handler.ProviderSet,
	))
}
```

#### 2. 配置文件

创建 `config.yaml`：

```yaml
app:
  stop_timeout: 30s

log:
  level: info
  filter_keys:
    - password
    - token

server:
  http:
    addr: :8080
  grpc:
    addr: :9090

database:
  default: main
  connections:
    main:
      driver: mysql
      dsn: "user:pass@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True"

redis:
  default: main
  connections:
    main:
      addr: localhost:6379

tracing:
  exporter:
    endpoint_url: http://localhost:4318/v1/traces
```

完整配置示例参考 [config.example.yaml](./config.example.yaml)

#### 3. 主程序

创建 `main.go`：

```go
package main

import (
	"flag"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
)

var configFile = flag.String("conf", "./config.yaml", "config path")

func main() {
	flag.Parse()
	bootstrap.Bootstrap(*configFile, wireApp)
}
```

#### 4. 生成代码并运行

```bash
# 生成 Wire 依赖注入代码
make generate

# 如果有 proto 文件，生成 proto 代码
make proto

# 运行应用
go run .
```

## 📁 项目结构

```
kratos-foundation/
├── cmd/                                         # 命令行工具
│   ├── protoc-gen-jsonschema/                   # JSON Schema 生成器
│   ├── protoc-gen-kratos-foundation-errors/     # 错误码生成器
│   └── protoc-gen-kratos-foundation-client/     # 客户端代码生成器
├── pkg/
│   ├── component/                               # 核心组件 (DI Provider)
│   │   ├── app/                                 # 应用生命周期管理
│   │   ├── client/                              # 服务客户端工厂
│   │   ├── consul/                              # Consul 客户端
│   │   ├── database/                            # 数据库连接池 (GORM)
│   │   ├── internal/                            # 内部中间件
│   │   │   ├── middleware/                      # 中间件实现
│   │   │   │   ├── circuitbreaker/              # 熔断
│   │   │   │   ├── logging/                     # 日志
│   │   │   │   ├── metadata/                    # 元数据传递
│   │   │   │   ├── ratelimit/                   # 限流
│   │   │   │   ├── timeout/                     # 超时控制
│   │   │   │   └── validator/                   # 参数校验
│   │   │   └── filter/                          # 过滤器
│   │   ├── job/                                 # 定时任务调度
│   │   │   ├── cron/                            # Cron 任务
│   │   │   ├── job/                             # Job 抽象
│   │   │   └── middleware/                      # 任务中间件
│   │   ├── log/                                 # 结构化日志
│   │   ├── metrics/                             # OpenTelemetry 指标
│   │   ├── redis/                               # Redis 连接池
│   │   ├── registry/                            # 服务注册
│   │   ├── server/                              # HTTP/gRPC/WebSocket 服务器
│   │   └── tracing/                             # 分布式追踪
│   ├── app_info/                                # 应用信息
│   ├── bootstrap/                               # 启动引导
│   ├── env/                                     # 环境检测
│   ├── errors/                                  # 错误处理
│   ├── transport/                               # 传输层工具
│   └── utils/                                   # 工具函数
├── proto/                                       # Protocol Buffers 定义
│   ├── config.proto                             # 配置 proto 定义
│   ├── conf.proto                               # 配置模板
│   └── kratos_foundation_pb/                    # 生成的 Go 代码
├── third_party/                                 # 第三方 proto 文件
│   ├── google/                                  # Google Proto
│   └── pubg/                                    # JSON Schema 选项
├── config.example.yaml                          # 配置示例
├── config.schema.json                           # 配置 JSON Schema
├── Makefile                                     # 构建脚本
└── README.md
```

## 📚 组件详解

### 🪵 日志 (Log)

结构化日志，支持多输出、自动字段注入和敏感信息过滤：

```yaml
log:
  level: info                    # 全局日志级别
  filter_empty: true             # 过滤空值
  filter_keys:                   # 敏感信息脱敏
    - password
    - token
  preset:                        # 预置字段
    - ts                         # 时间戳
    - service.id                 # 服务 ID
    - trace.id                   # TraceID
    - caller                     # 调用位置

  std:                           # 标准输出
    disable: false

  file:                          # 文件输出
    disable: false
    path: ./logs/app.log
    rotating:
      max_size: 100              # MB
      max_file_age: 7            # 天
      max_files: 10
```

### 💾 数据库 (Database)

GORM 集成，支持多数据库、主从分离：

```yaml
database:
  default: main
  connections:
    main:
      driver: mysql              # mysql/sqlite/postgres
      dsn: "..."
      replicas:                  # 从库（读写分离）
        - driver: mysql
          dsn: "..."
      max_idle_conns: 10
      max_open_conns: 100
      conn_max_lifetime: 1h
  gorm:
    logger:
      level: WARN
      slow_threshold: 200ms
  tracing:
    disable: false
  metrics:
    disable: false
```

代码示例：

```go
type UserRepo struct {
	db *gorm.DB
}

func NewUserRepo(dbs *database.Databases) *UserRepo {
	return &UserRepo{
		db: dbs.Default(), // 获取默认数据库
	}
}
```

### 📮 Redis

多连接池管理，自动集成追踪与指标：

```yaml
redis:
  default: main
  connections:
    main:
      addr: localhost:6379
      pool_size: 100
      min_idle_conns: 10
    cache:                       # 多 Redis 实例
      addr: localhost:6380
      db: 1
  tracing:
    disable: false
  metrics:
    disable: false
```

代码示例：

```go
type CacheRepo struct {
	rdb redis.Cmdable
}

func NewCacheRepo(rdbs *redis.Redis) *CacheRepo {
	return &CacheRepo{
		rdb: rdbs.Default(), // 获取默认 Redis
	}
}
```

### 🌐 服务器 (Server)

HTTP 与 gRPC 双协议支持，统一中间件：

```yaml
server:
  stop_delay: 3s               # 停机延迟
  middleware:
    timeout:
      default: 1s
      routes:                  # 路由级超时配置
        - path: /api.v1.Service/LongRunning
          timeout: 30s
    tracing:
      disable: false
    metrics:
      disable: false
    logging:
      disable: false
    ratelimit:
      enable: false

  http:
    addr: :8080
    timeout: 3s
    metrics:
      path: /metrics

  grpc:
    addr: :9090
    timeout: 3s
```

### 🔌 客户端 (Client)

服务发现与直连模式，支持熔断、重试：

```yaml
client:
  clients:
    user-service:                # 服务发现模式
      protocol: GRPC
      target: discovery:///user-service
      middleware:
        timeout:
          default: 2s
        circuitbreaker:
          enable: true

    external-api:                # 直连模式
      protocol: HTTPS
      target: api.example.com:443
```

代码示例：

```go
type UserServiceClient struct {
	client userpb.UserServiceClient
}

func NewUserServiceClient(factory *client.Factory) (*UserServiceClient, error) {
	conn, err := factory.GetClient("user-service")
	if err != nil {
		return nil, err
	}
	return &UserServiceClient{
		client: userpb.NewUserServiceClient(conn),
	}, nil
}
```

### ⏰ 定时任务 (Job)

Cron 调度，支持并发策略控制：

```yaml
job:
  timezone: Asia/Shanghai
  jobs:
    sync-data:
      schedule: "0 * * * * *"          # 秒 分 时 日 月 周
      concurrent_policy: SKIP           # SKIP/OVERLAP/DELAY
      immediately: false                # 启动时立即执行
    cleanup:
      schedule: "@hourly"               # 预定义表达式
    health-check:
      schedule: "@every 30s"
```

代码示例：

```go
type DataSyncJob struct{}

func (j *DataSyncJob) Name() string {
	return "sync-data"
}

func (j *DataSyncJob) Run(ctx context.Context) error {
	// 任务逻辑
	return nil
}

// 在 Wire 中注册
func NewJobs() []job.Job {
	return []job.Job{
		&DataSyncJob{},
	}
}
```

### 📡 链路追踪 (Tracing)

OpenTelemetry 集成，支持采样策略：

```yaml
tracing:
  disable: false
  exporter:
    endpoint_url: http://localhost:4318/v1/traces
    compression: GZIP
    timeout: 10s
    retry:
      enabled: true
  sampler:
    sample: RATIO
    ratio: 0.05                # 5% 采样率
```

### 📝 服务注册 (Registry)

Consul 服务注册与发现：

```yaml
consul:
  addr: localhost:8500
  scheme: http

registry:
  disable: false
```

## 🛠️ protoc-gen-jsonschema 工具

将 Protocol Buffer 定义转换为 JSON Schema。

### 安装

```bash
go install github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema@main
```

### 快速使用

```bash
# 基础生成
protoc --jsonschema_out=. *.proto

# 生成 YAML 格式
protoc --jsonschema_out=. --jsonschema_opt=output_file_suffix=.yaml *.proto

# 压缩输出（适合网络传输）
protoc --jsonschema_out=. --jsonschema_opt=pretty_json_output=false *.proto

# 符合 ProtoJSON 标准（int64 转 string）
protoc --jsonschema_out=. \
  --jsonschema_opt=respect_protojson_int64=true \
  --jsonschema_opt=respect_protojson_presence=true \
  *.proto
```

### 核心特性

- **多版本支持** - Draft-04/06/07/2019-09/2020-12
- **Proto2/Proto3 兼容**
- **Well-Known Types** - 内置 Google Protobuf 和 Kubernetes 类型支持
- **自定义选项** - 字段级、消息级、文件级配置
- **四阶段架构**：
  1. Frontend Generator - Proto 解析与中间 Schema 生成
  2. Backend Optimizer - 未使用定义移除（Tree Shaking）
  3. Target Generator - 目标 Draft 版本生成
  4. Serializer - JSON/YAML 序列化

详细文档请参考 [cmd/protoc-gen-jsonschema/README.md](./cmd/protoc-gen-jsonschema/README.md)

## 🏗️ 构建命令

```bash
# 安装依赖工具
make init

# 生成代码 (Wire + go generate)
make generate

# 生成 Proto 文件
make proto

# 代码检查
make lint

# 全部执行
make all
```

## 📦 核心依赖

| 组件                          | 版本      | 用途          |
|-----------------------------|---------|-------------|
| go-kratos/kratos            | v2.9.1  | 微服务框架       |
| google/wire                 | v0.7.0  | 依赖注入        |
| gorm.io/gorm                | v1.31.1 | ORM         |
| redis/go-redis              | v9.17.0 | Redis 客户端  |
| go.opentelemetry.io/otel    | v1.38.0 | 可观测性        |
| hashicorp/consul/api        | v1.26.1 | 服务发现        |
| robfig/cron                 | v3.0.1  | 定时任务        |
| go.uber.org/zap             | v1.27.0 | 日志          |
| google.golang.org/protobuf  | v1.36.8 | Protocol Buffer |
| google.golang.org/grpc      | v1.75.0 | gRPC        |

## 📖 最佳实践

### 1. 配置管理

- 使用 `config.schema.json` 验证配置文件
- 敏感信息使用环境变量或配置中心
- 区分环境配置（dev/test/prod）

### 2. 日志规范

- 使用结构化日志，避免字符串拼接
- 敏感信息脱敏（通过 `filter_keys`）
- 合理设置日志级别

### 3. 错误处理

- 使用 `pkg/errors` 包装错误，保留堆栈
- 定义业务错误码（通过 proto 生成）
- 区分可恢复错误与不可恢复错误

### 4. 性能优化

- 数据库使用连接池
- Redis 使用 Pipeline 批量操作
- gRPC 启用连接复用
- 合理设置超时时间

### 5. 可观测性

- 启用分布式追踪
- 监控关键指标（请求量、延迟、错误率）
- 设置合理的采样率

## 🤝 贡献指南

欢迎贡献代码、报告 Bug 或提出新特性建议！

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'feat: add some amazing feature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启 Pull Request

提交信息请遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范。

## 📄 许可证

[Apache License 2.0](LICENSE)

## 🙏 致谢

- [Kratos](https://github.com/go-kratos/kratos) - 优秀的 Go 微服务框架
- [protoc-gen-jsonschema (PUBG)](https://github.com/pubg/protoc-gen-jsonschema) - JSON Schema 生成器原型

---

<a name="english"></a>

# Kratos Foundation

A production-grade infrastructure library based on [Kratos](https://github.com/go-kratos/kratos) microservice framework, providing out-of-the-box enterprise-level microservice development capabilities.

## ✨ Core Features

### 🏗️ Complete Microservice Infrastructure
- **Service Discovery & Registration** - Consul integration with automatic registration and health checks
- **Distributed Tracing** - OpenTelemetry-based with OTLP HTTP exporter
- **Metrics Collection** - Prometheus metrics with auto-instrumentation for HTTP/gRPC/Database/Cache
- **Structured Logging** - Log filtering, rotation, multiple outputs, auto TraceID injection
- **Dependency Injection** - Compile-time DI based on Google Wire

### 💾 Data Layer
- **Database Access** - GORM integration supporting multiple databases (MySQL/PostgreSQL/SQLite), read-write separation, tracing
- **Redis Cache** - Multiple connection pool management with tracing and metrics

### 🌐 Service Layer
- **Dual Protocol Server** - HTTP and gRPC servers with WebSocket support
- **Client Factory** - Service discovery and direct connection modes with circuit breaker, retry, timeout policies
- **Middleware System** - Unified server/client middleware (logging, tracing, rate limiting, circuit breaking, etc.)

### ⏰ Task Scheduling
- **Scheduled Jobs** - Cron scheduling with concurrent policy control (SKIP/OVERLAP/DELAY)

### 🛠️ Development Tools
- **protoc-gen-jsonschema** - Protocol Buffer to JSON Schema converter (supports Draft-04/06/07/2019-09/2020-12)
- **protoc-gen-kratos-foundation-errors** - Error code generator
- **protoc-gen-kratos-foundation-client** - Client code generator

## 📋 Requirements

- Go >= 1.23
- Protocol Buffers compiler (protoc)

## 🚀 Quick Start

### Installation

```bash
go get github.com/jaggerzhuang1994/kratos-foundation
```

### Install Development Tools

```bash
make init
```

For detailed usage, please refer to the Chinese documentation above.

## 📦 Core Dependencies

| Component                    | Version | Purpose  |
|------------------------------|---------|----------|
| go-kratos/kratos             | v2.9.1  | Microservice Framework |
| google/wire                  | v0.7.0  | Dependency Injection |
| gorm.io/gorm                 | v1.31.1 | ORM |
| redis/go-redis               | v9.17.0 | Redis Client |
| go.opentelemetry.io/otel     | v1.38.0 | Observability |
| hashicorp/consul/api         | v1.26.1 | Service Discovery |
| robfig/cron                  | v3.0.1  | Cron Scheduler |
| go.uber.org/zap              | v1.27.0 | Logger |

## 📄 License

[Apache License 2.0](LICENSE)
