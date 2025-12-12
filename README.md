# Kratos Foundation

基于 [Kratos](https://github.com/go-kratos/kratos) 微服务框架的生产级基础设施库，提供开箱即用的企业级微服务开发能力。

## 特性

- **服务发现与注册** - 集成 Consul，支持服务自动注册、发现和健康检查
- **分布式追踪** - 基于 OpenTelemetry，支持 OTLP HTTP 导出器
- **指标采集** - Prometheus 指标，自动埋点 HTTP/gRPC/数据库/缓存
- **结构化日志** - 支持日志过滤、轮转、多输出目标，自动注入 TraceID
- **数据库访问** - GORM 集成，支持多数据库、读写分离、链路追踪
- **Redis 缓存** - 多连接池管理，集成追踪与指标
- **定时任务** - Cron 调度，支持并发策略控制
- **双协议服务** - HTTP 与 gRPC 服务器，支持 WebSocket
- **依赖注入** - 基于 Google Wire 的编译时 DI

## 环境要求

- Go >= 1.23
- Protocol Buffers 编译器 (protoc)

## 安装

```bash
go get github.com/jaggerzhuang1994/kratos-foundation
```

安装开发工具：

```bash
make init
```

## 快速开始

### 1. 项目初始化

使用 Google Wire 组装依赖：

```go
//go:build wireinject

package main

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component"
)

func wireApp() (*kratos.App, func(), error) {
	panic(wire.Build(
		component.ProviderSet,
		// 业务 ProviderSet...
	))
}
```

### 2. 配置文件

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

### 3. 启动应用

```go
package main

func main() {
	app, cleanup, err := wireApp()
	if err != nil {
		panic(err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

## 项目结构

```
kratos-foundation/
├── cmd/                                    # 命令行工具
│   ├── protoc-gen-kratos-foundation-errors/   # 错误码生成器
│   └── protoc-gen-kratos-foundation-client/   # 客户端代码生成器
├── pkg/
│   ├── component/                          # 核心组件 (DI Provider)
│   │   ├── app/                            # 应用生命周期
│   │   ├── client/                         # 服务客户端工厂
│   │   ├── config/                         # 配置加载
│   │   ├── consul/                         # Consul 客户端
│   │   ├── database/                       # 数据库连接 (GORM)
│   │   ├── job/                            # 定时任务调度
│   │   ├── log/                            # 结构化日志
│   │   ├── metrics/                        # OpenTelemetry 指标
│   │   ├── redis/                          # Redis 连接
│   │   ├── registry/                       # 服务注册
│   │   ├── server/                         # HTTP/gRPC 服务器
│   │   └── tracing/                        # 分布式追踪
│   ├── env/                                # 环境检测
│   ├── errors/                             # 错误处理
│   └── utils/                              # 工具函数
├── proto/                                  # Protocol Buffers 定义
├── config.example.yaml                     # 配置示例
└── Makefile                                # 构建脚本
```

## 组件说明

### 日志 (Log)

结构化日志，支持多输出和自动字段注入：

```yaml
log:
  level: info
  filter_empty: true
  filter_keys: [ password, token, secret ]
  preset: [ ts, service.id, trace.id, caller ]
  std:
    disable: false
  file:
    disable: false
    path: ./logs/app.log
    rotating:
      max_size: 100      # MB
      max_file_age: 7    # 天
      max_files: 10
```

### 数据库 (Database)

GORM 集成，支持主从分离：

```yaml
database:
  default: main
  connections:
    main:
      driver: mysql  # mysql, sqlite, postgres
      dsn: "..."
      replicas:
        - driver: mysql
          dsn: "..."
  gorm:
    logger:
      level: WARN
      slow_threshold: 200ms
  tracing:
    disable: false
  metrics:
    disable: false
```

### Redis

多连接池管理：

```yaml
redis:
  default: main
  connections:
    main:
      addr: localhost:6379
      pool_size: 100
      min_idle_conns: 10
    cache:
      addr: localhost:6380
      db: 1
  tracing:
    disable: false
  metrics:
    disable: false
```

### 服务器 (Server)

HTTP 与 gRPC 双协议支持：

```yaml
server:
  stop_delay: 3s
  middleware:
    timeout:
      default: 1s
      routes:
        - path: /api.v1.LongRunning/Process
          timeout: 30s
    tracing:
      disable: false
    metrics:
      disable: false
    ratelimit:
      enable: false
  http:
    addr: :8080
    metrics:
      path: /metrics
  grpc:
    addr: :9090
```

### 客户端 (Client)

服务发现与直连模式：

```yaml
client:
  clients:
    user-service:
      protocol: GRPC
      target: discovery:///user-service
      middleware:
        timeout:
          default: 2s
        circuitbreaker:
          enable: true
    external-api:
      protocol: HTTPS
      target: api.example.com:443
```

### 定时任务 (Job)

Cron 调度与并发策略：

```yaml
job:
  timezone: Asia/Shanghai
  jobs:
    sync-data:
      schedule: "0 * * * * *"      # 秒 分 时 日 月 周
      concurrent_policy: SKIP       # SKIP, OVERLAP, DELAY
      immediately: false
    cleanup:
      schedule: "@hourly"
    health-check:
      schedule: "@every 30s"
      immediately: true
```

### 链路追踪 (Tracing)

OpenTelemetry 集成：

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
    ratio: 0.05  # 5% 采样
```

## 构建命令

```bash
# 安装依赖工具
make init

# 生成代码 (Wire, go generate)
make generate

# 生成 Proto
make proto

# 代码检查
make lint

# 全部执行
make all
```

## 核心依赖

| 组件                       | 版本      |
|--------------------------|---------|
| go-kratos/kratos         | v2.9.1  |
| google/wire              | v0.7.0  |
| gorm.io/gorm             | v1.31.1 |
| redis/go-redis           | v9.17.0 |
| go.opentelemetry.io/otel | v1.38.0 |
| hashicorp/consul/api     | v1.26.1 |
| robfig/cron              | v3.0.1  |
| go.uber.org/zap          | v1.27.0 |

## 许可证

[Apache License 2.0](LICENSE)
