# Kratos Foundation

[![Go Version](https://img.shields.io/badge/Go-1.23.6-blue)](https://go.dev/)
[![Kratos](https://img.shields.io/badge/Kratos-v2.9.1-orange)](https://go-kratos.dev/)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

Kratos Foundation 是一个基于 [Go-Kratos](https://go-kratos.dev/) 框架的企业级微服务基础库，提供了一套完整的生产就绪功能模块，帮助开发者快速构建可扩展、可观测的微服务应用。

## 特性

- 🚀 **开箱即用** - 提供企业级微服务常用功能模块，按需配置
- 📦 **统一配置** - 基于 Protobuf 的配置定义，强类型且自动验证
- 🔍 **可观测性** - 内置日志、监控指标、链路追踪完整方案
- 🛠️ **依赖注入** - 基于 Wire 的编译时依赖注入
- 🌐 **服务治理** - 集成 Consul 服务注册、发现与配置中心
- 💾 **数据访问** - GORM ORM 集成，支持主从数据库
- ⏰ **定时任务** - 基于 Cron 的定时任务调度，支持并发策略
- 🔄 **RPC 客户端** - HTTP/gRPC 客户端工厂，支持服务发现与负载均衡

## 功能模块

| 模块 | 说明 | 状态 |
|------|------|------|
| **应用管理** (`pkg/app`) | 应用生命周期管理、元信息 | ✅ 稳定 |
| **日志** (`pkg/log`) | 结构化日志、文件轮转、多输出 | ✅ 稳定 |
| **监控** (`pkg/metrics`) | Prometheus 指标采集与导出 | ✅ 稳定 |
| **链路追踪** (`pkg/tracing`) | OpenTelemetry 分布式追踪 | ✅ 稳定 |
| **HTTP 服务器** (`pkg/server/http`) | HTTP 服务器、WebSocket | ✅ 稳定 |
| **gRPC 服务器** (`pkg/server/grpc`) | gRPC 服务器、反射服务 | ✅ 稳定 |
| **数据库** (`pkg/database`) | GORM、主从分离、连接池 | ✅ 稳定 |
| **Redis** (`pkg/redis`) | Redis 客户端、集群支持 | ✅ 稳定 |
| **服务发现** (`pkg/discovery`) | Consul 服务发现 | ✅ 稳定 |
| **服务注册** (`pkg/registry`) | Consul 服务注册 | ✅ 稳定 |
| **配置中心** (`pkg/config`) | Consul KV 配置源 | ✅ 稳定 |
| **RPC 客户端** (`pkg/client`) | HTTP/gRPC 客户端工厂 | ✅ 稳定 |
| **定时任务** (`pkg/job`) | Cron 任务调度 | ✅ 稳定 |
| **中间件** (`internal/middleware`) | 通用中间件集合 | ✅ 稳定 |

## 快速开始

### 环境要求

- Go >= 1.23.6
- Protoc >= 3.x
- Wire (编译时安装)
- Consul (可选，用于服务治理)

### 安装

```bash
go get github.com/jaggerzhuang1994/kratos-foundation
```

### 初始化开发工具

```bash
# 安装所有必需的工具
make init
```

这将安装以下工具：
- `wire` - 依赖注入代码生成器
- `protoc` 相关插件 - Protobuf 代码生成
- `kratos` - Kratos CLI 工具
- `golangci-lint` - 代码检查工具

### 基本使用

#### 1. 定义配置

在 `proto/config.proto` 中定义你的配置：

```protobuf
syntax = "proto3";
package kratos_foundation_pb;

import "config_pb/app.proto";
import "config_pb/server.proto";
import "config_pb/database.proto";
// ... 其他模块

message Config {
  App app = 1;
  Server server = 2;
  Database database = 3;
  // ... 其他配置
}
```

#### 2. 生成配置代码

```bash
make proto
```

这将生成：
- Protobuf Go 代码
- 配置 JSON Schema (`config.schema.json`)

#### 3. 创建应用主入口

```go
package main

import (
	"flag"

	"github.com/jaggerzhuang1994/kratos-foundation-template/internal/conf"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	_ "github.com/jaggerzhuang1994/kratos-foundation/pkg/setup"
	_ "go.uber.org/automaxprocs"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Version is the version of the compiled software.
	Version string
	// flagconf is the config flag.
	flagconf string
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

func main() {
	flag.Parse()

	// wireApp
	app, cleanup, err := wireApp(app_info.Version(Version), conf.FileConfigSource(flagconf))
	if err != nil {
		panic(err)
	}
	defer cleanup()

	// start and wait for stop signal
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

#### 4. 生成依赖注入代码

```bash
make generate
```

#### 5. 配置文件示例

参考 `config.example.yaml` 创建你的配置文件：

```yaml
app:
  name: my-service
  version: v1.0.0

server:
  http:
    addr: 0.0.0.0:8000
  grpc:
    addr: 0.0.0.0:9000

database:
  default: primary
  connections:
    primary:
      dsn: root:password@tcp(127.0.0.1:3306)/mydb
      driver: mysql
```

## 配置模块详解

### 应用配置 (App)

```yaml
app:
  name: my-service          # 服务名称
  version: v1.0.0          # 版本
  metadata:                # 元数据（会注册到服务发现）
    env: production
    region: cn-north
```

### 日志配置 (Log)

```yaml
log:
  level: info              # 日志级别: debug/info/warn/error
  std:                     # 标准输出
    disable: false
  file:                    # 文件输出
    path: ./logs/app.log
    rotating:
      max_size: 100        # MB
      max_age: 30          # days
      compress: true
```

### 监控配置 (Metrics)

```yaml
metrics:
  meter_name: my-service   # 指标命名空间
```

访问 `http://localhost:8000/metrics` 查看 Prometheus 指标。

### 链路追踪配置 (Tracing)

```yaml
tracing:
  disable: false
  exporter:
    endpoint_url: http://jaeger:14268/api/traces
    compression: GZIP
  sampler:
    sample: RATIO          # 采样策略: ALWAYS/NEVER/RATIO
    ratio: 0.1            # 采样率 10%
```

### 服务器配置 (Server)

```yaml
server:
  http:
    addr: 0.0.0.0:8000
    timeout: 30s
    middleware:
      logging:
        disable: false
      metrics:
        disable: false
      tracing:
        disable: false
  grpc:
    addr: 0.0.0.0:9000
    timeout: 30s
```

### 数据库配置 (Database)

```yaml
database:
  default: primary
  connections:
    primary:
      dsn: root:password@tcp(127.0.0.1:3306)/mydb
      driver: mysql
      max_open_conns: 100
      max_idle_conns: 10
      conn_max_lifetime: 1h
    replica:
      - dsn: root:password@tcp(127.0.0.1:3307)/mydb
        driver: mysql
  gorm:
    skip_default_transaction: true
    logger:
      level: Warn
      slow_threshold: 200ms
```

### Redis 配置 (Redis)

```yaml
redis:
  default: cache
  connections:
    cache:
      addr: 127.0.0.1:6379
      password: ""
      db: 0
      pool_size: 10
      read_timeout: 3s
      write_timeout: 3s
```

### 服务发现与注册 (Discovery & Registry)

```yaml
discovery:
  timeout: 10s

registry:
  disable_health_check: false
  healthcheck_internal: 10s
  tags:
    - production
    - v1
```

### 客户端配置 (Client)

```yaml
client:
  clients:
    user-service:
      target: discovery:///user-service  # 服务发现
      protocol: GRPC
      middleware:
        timeout:
          default: 5s
        tracing:
          disable: false
        metrics:
          disable: false
```

### 定时任务配置 (Job)

```yaml
job:
  timezone: Asia/Shanghai
  jobs:
    cleanup:
      schedule: "@daily"              # 每天 0 点
      immediately: true               # 启动时立即执行一次
      concurrent_policy: SKIP         # 并发策略: OVERLAP/DELAY/SKIP
    backup:
      schedule: "0 2 * * *"          # 每天凌晨 2 点
```

## 常用命令

```bash
# 生成 Proto 代码和配置 Schema
make proto

# 生成所有代码 (Wire 等)
make generate

# 运行代码检查
make lint

# 一次性执行所有命令
make all
```

## 项目结构

```
kratos-foundation/
├── cmd/                    # 命令行工具和代码生成器
│   ├── protoc-gen-kratos-foundation-client/
│   └── protoc-gen-jsonschema/
├── internal/               # 内部实现
│   ├── middleware/         # 中间件实现
│   └── logger/            # 日志实现
├── pkg/                    # 公共 API (可被外部依赖)
│   ├── app/               # 应用管理
│   ├── app_info/          # 应用元信息
│   ├── client/            # RPC 客户端
│   ├── config/            # 配置加载
│   ├── consul/            # Consul 客户端
│   ├── database/          # 数据库
│   ├── discovery/         # 服务发现
│   ├── env/               # 环境变量
│   ├── errors/            # 错误处理
│   ├── job/               # 定时任务
│   ├── log/               # 日志
│   ├── metrics/           # 监控指标
│   ├── redis/             # Redis
│   ├── registry/          # 服务注册
│   ├── server/            # HTTP/gRPC 服务器
│   ├── tracing/           # 链路追踪
│   ├── transport/         # 传输层
│   └── utils/             # 工具函数
├── proto/                  # Protobuf 定义
│   ├── config.proto       # 主配置
│   ├── config_pb/         # 配置子模块
│   └── error_reason.proto # 错误定义
├── third_party/           # 第三方 proto 定义
├── config.example.yaml    # 配置示例
├── config.schema.json     # 配置 JSON Schema
├── Makefile               # 构建脚本
└── go.mod                 # Go 模块定义
```

## 中间件

框架提供以下中间件：

### 服务端中间件

- **Timeout** - 超时控制，支持按路由配置
- **Metrics** - Prometheus 指标采集
- **Tracing** - OpenTelemetry 链路追踪
- **Logging** - 结构化日志记录
- **Metadata** - 元数据传递
- **Validator** - 请求参数验证
- **RateLimit** - BBR 自适应限流
- **CircuitBreaker** - SRE 熔断器

### 客户端中间件

- **Timeout** - 超时控制
- **Metrics** - 客户端指标
- **Tracing** - 链路追踪上下文传递
- **Logging** - 请求/响应日志
- **CircuitBreaker** - 客户端熔断

## 依赖注入

Kratos Foundation 使用 [Wire](https://github.com/google/wire) 进行编译时依赖注入。

```go
//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built in the final build.

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation-template/internal"
	"github.com/jaggerzhuang1994/kratos-foundation-template/internal/conf"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
)

// wireApp init kratos application.
func wireApp(app_info.Version, conf.FileConfigSource) (*kratos.App, func(), error) {
	panic(wire.Build(
		kratos_foundation.ProviderSet,
		internal.ProviderSet,
		NewBootstrap,
	))
}

```

## 可观测性

### 日志

结构化日志，支持 JSON 格式输出，自动注入：
- 时间戳
- Trace ID / Span ID
- 服务名称 / 版本
- 调用位置

### 监控指标

Prometheus 指标包括：
- HTTP/gRPC 请求计数、延迟
- 数据库连接池、查询统计
- Redis 操作统计
- 定时任务执行统计

### 链路追踪

OpenTelemetry 集成，支持导出到：
- Jaeger
- Zipkin
- OTLP-compatible 系统

## 最佳实践

1. **配置管理** - 使用环境变量覆盖配置，敏感信息通过环境变量注入
2. **错误处理** - 使用定义的 Error Reason 统一错误码
3. **日志规范** - 保持日志结构化，避免打印敏感信息
4. **资源管理** - 合理配置连接池大小，设置合理的超时时间
5. **监控告警** - 关键指标配置告警规则
6. **优雅停机** - 实现 `Stop` 方法处理优雅关闭

## 贡献指南

欢迎贡献代码！请确保：

1. 通过 `make lint` 代码检查
2. 添加必要的单元测试
3. 更新相关文档
4. 遵循现有代码风格

## 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件

## 相关链接

- [Go-Kratos 官方文档](https://go-kratos.dev/)
- [Wire 文档](https://github.com/google/wire)
- [GORM 文档](https://gorm.io/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)
- [Consul 文档](https://www.consul.io/docs)

## 支持

如有问题或建议，欢迎提交 Issue 或 Pull Request。
