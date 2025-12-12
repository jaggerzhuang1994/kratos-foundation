# kratos-foundation

Kratos 框架的基础组件库，提供开箱即用的企业级组件封装。

## 特性

- **开箱即用** - Wire 依赖注入，零配置启动
- **可观测性** - 内置 Log / Metrics / Tracing 三件套（OpenTelemetry）
- **服务治理** - 限流、熔断、超时、重试、元数据传递
- **多协议支持** - HTTP / gRPC / WebSocket
- **配置管理** - 支持文件和 Consul，环境隔离
- **定时任务** - Cron 表达式，并发策略控制

## 安装

```bash
go get github.com/jaggerzhuang1994/kratos-foundation
```

## 组件列表

| 组件           | 说明                      | 路径                       |
|--------------|-------------------------|--------------------------|
| **app**      | 应用生命周期、Hook 机制          | `pkg/component/app`      |
| **config**   | 配置管理（文件/Consul）         | `pkg/component/config`   |
| **consul**   | Consul 客户端              | `pkg/component/consul`   |
| **log**      | 结构化日志（std/file，轮转）      | `pkg/component/log`      |
| **metrics**  | 指标监控（OpenTelemetry）     | `pkg/component/metrics`  |
| **tracing**  | 分布式追踪（OpenTelemetry）    | `pkg/component/tracing`  |
| **server**   | HTTP/gRPC/WebSocket 服务器 | `pkg/component/server`   |
| **client**   | HTTP/gRPC 客户端工厂         | `pkg/component/client`   |
| **database** | GORM 封装（读写分离）           | `pkg/component/database` |
| **redis**    | Redis 客户端               | `pkg/component/redis`    |
| **job**      | 定时任务/后台任务               | `pkg/component/job`      |
| **registry** | 服务注册与发现                 | `pkg/component/registry` |

## 快速开始

### 最小配置

```yaml
# $schema: https://raw.githubusercontent.com/jaggerzhuang1994/kratos-foundation/main/config.schema.json

log:
  level: info

server:
  http:
    addr: :8080
  grpc:
    addr: :9090
```

### Wire 注入

```go
//go:build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component"
)

func initApp() (*kratos.App, func(), error) {
	wire.Build(
		component.ProviderSet,
		// 必须还要提供 bootstrap.Bootstrap/config.FileConfigSource/config.ConsulConfigSource 的provider
		// 其他providers...
	)
	return nil, nil, nil
}

```

## 环境变量

| 变量名              | 说明                                              | 默认值     |
|------------------|-------------------------------------------------|---------|
| `APP_ENV`        | 运行环境: `local` / `dev` / `test` / `pre` / `prod` | `local` |
| `DISABLE_CONSUL` | 禁用 Consul 连接                                    | `false` |
| CONSUL_*         | consul 相关配置                                     | -       |

## 配置参考

<details>
<summary><b>App 组件</b></summary>

```yaml
app:
  disable_registrar: false    # [默认:false] 是否禁用服务注册
  registrar_timeout: 10s      # [默认:10s] 注册中心超时时间
  stop_timeout: 30s           # [默认:30s] 应用停止超时（需大于 server.stop_delay）
```

</details>

<details>
<summary><b>Log 组件</b></summary>

```yaml
log:
  level: info                 # [默认:info(prod)/debug(local)] 可选: debug/info/warn/error
  filter_empty: true          # [默认:true] 是否过滤空值 kv
  filter_keys: [ password, token ]  # [默认:[]] 过滤的字段名
  time_format: "2006-01-02T15:04:05Z07:00"  # [默认:RFC3339] Go time format
  preset:                     # [默认:全部] 预置字段
    - ts                      # 时间戳
    - service.id              # 服务ID
    - service.name            # 服务名
    - service.version         # 服务版本
    - trace.id                # 链路ID
    - span.id                 # SpanID
    - caller                  # 调用位置
  std:                        # 标准输出日志
    disable: false            # [默认:false]
    level: info               # [默认:继承上层]
    filter_empty: true        # [默认:继承上层]
    filter_keys: [ ]          # [默认:[]] 会合并上层
  file:                       # 文件日志
    disable: false            # [默认:false]
    level: info               # [默认:继承上层]
    path: ./logs/app.log      # [默认:./app.log] 日志文件路径
    rotating:                 # 日志轮转
      disable: false          # [默认:false]
      max_size: 100           # [默认:100] 单文件最大 MB
      max_file_age: 7         # [默认:无限制] 保留天数
      max_files: 10           # [默认:无限制] 最大文件数
      local_time: true        # [默认:false] 使用本地时间命名
      compress: true          # [默认:false] gzip 压缩
```

</details>

<details>
<summary><b>Metrics 组件</b></summary>

```yaml
metrics:
  meter_name: my_app          # [默认:应用名] 指标命名空间
  counter_map_size: 64        # [默认:64] counter map 初始容量
  gauge_map_size: 64          # [默认:64] gauge map 初始容量
  histogram_map_size: 64      # [默认:64] histogram map 初始容量
  log:
    level: info               # [默认:继承] 模块日志级别
```

</details>

<details>
<summary><b>Tracing 组件</b></summary>

```yaml
tracing:
  disable: false              # [默认:false]
  tracer_name: my_app         # [默认:应用名]
  exporter:
    endpoint_url: http://localhost:4317  # OTLP 端点
    compression: GZIP         # [默认:NO] 可选: NO/GZIP
    timeout: 10s              # 导出超时
    headers:                  # 自定义 HTTP headers
      Authorization: Bearer xxx
    retry:
      enabled: true           # [默认:false]
      initial_interval: 1s    # 初始重试间隔
      max_interval: 30s       # 最大重试间隔
      max_elapsed_time: 5m    # 最大重试总时间
  sampler:
    sample: RATIO             # [默认:RATIO] 可选: RATIO/ALWAYS/NEVER
    ratio: 0.1                # [默认:0.05] 采样率 0-1
  log:
    level: info
```

</details>

<details>
<summary><b>Server 组件</b></summary>

```yaml
server:
  stop_delay: 5s              # [默认:0] 停止延迟（支持服务发现延迟）
  log:
    level: info
  http:
    disable: false            # [默认:false]
    network: tcp              # [默认:tcp] 可选: tcp/tcp4/tcp6/unix
    addr: :8080               # 监听地址 host:port
    endpoint:                 # 对外暴露端点（用于服务注册）
      scheme: http            # 可选: http/https
      host: localhost:8080
    disable_strict_slash: false  # [默认:false] 禁用 strictSlash
    path_prefix: /api         # HTTP 路由前缀
    metrics:
      disable: false          # [默认:false]
      path: /metrics          # [默认:/metrics]
  grpc:
    disable: false            # [默认:false]
    network: tcp              # [默认:tcp]
    addr: :9090               # 监听地址
    endpoint:
      scheme: grpc            # 可选: grpc/grpcs
      host: localhost:9090
    custom_health: false      # [默认:false] 自定义健康检查
    disable_reflection: false # [默认:false] 禁用服务反射
  middleware:
    timeout:
      default: 1s             # [默认:1s] 服务端默认超时
      routes:                 # 路由级超时（path 优先于 prefix）
        - path: /pb.Service/LongOp      # 精确匹配
          timeout: 30s
        - prefix: /pb.Service           # 前缀匹配
          timeout: 10s
    metadata:
      disable: false          # [默认:false]
      prefix: [ x-md- ]       # [默认:[x-md-]] 注入到 ctx 的前缀
      constants:              # 固定携带的 metadata
        x-md-global-app: my-app
    tracing:
      disable: false          # [默认:false]
      tracer_name: my_app     # [默认:继承 tracing 组件]
    metrics:
      disable: false          # [默认:false]
      meter_name: my_app      # [默认:继承 metrics 组件]
    logging:
      disable: false          # [默认:false]
    validator:
      disable: false          # [默认:false] 表单校验
    ratelimit:
      enable: false           # [默认:false] 限流器
      bbr_limiter:
        window: 10s           # [默认:10s] 窗口时间
        bucket: 100           # [默认:100] 桶数量
        cpu_threshold: 800    # [默认:800] CPU 阈值
        cpu_quota: 0.5        # [可选] CPU 配额
```

</details>

<details>
<summary><b>Client 组件</b></summary>

```yaml
client:
  log:
    level: info
  clients:
    user_service:             # 客户端名称（代码中使用）
      protocol: GRPC          # [默认:GRPC] 可选: GRPC/HTTP/GRPCS/HTTPS
      target: discovery:///user-service  # [默认:discovery:///{client_key}]
      # target: localhost:9090  # 直连模式
      middleware:
        timeout:
          default: 2s         # [默认:2s] 客户端默认超时
          routes:
            - path: /user.Service/GetList
              timeout: 10s
        metadata:
          disable: false
          prefix: [ x-md-global- ]  # [默认:[x-md-global-]] 传递给下游的前缀
          constants:
            x-md-caller: my-app
        tracing:
          disable: false
        metrics:
          disable: false
        logging:
          disable: false
        circuitbreaker:       # 熔断器
          enable: false       # [默认:false]
          sre:
            success: 0.6      # [默认:0.6] K = 1/Success
            request: 100      # [默认:100] 最小请求数
            bucket: 10        # [默认:10] 桶数量
            window: 3s        # [默认:3s] 窗口时间
```

</details>

<details>
<summary><b>Database 组件</b></summary>

```yaml
database:
  default: primary            # 默认连接名
  log:
    level: info
  connections:
    primary:
      driver: mysql           # [默认:mysql] 可选: mysql/postgres/sqlite
      dsn: "user:pass@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True"
      replicas:               # 从库（读写分离）
        - driver: mysql
          dsn: "user:pass@tcp(replica:3306)/db"
      datas: [ statistics ]   # 自动切换数据源的表名
      trace_resolver_mode: false  # [默认:false] 打印读写分离日志
  gorm:
    skip_default_transaction: false  # [默认:false] 跳过默认事务
    default_transaction_timeout: 30s # 默认事务超时
    default_context_timeout: 10s     # 默认上下文超时
    create_batch_size: 100    # 批量创建大小
    translate_error: true     # [默认:false] 翻译错误
    logger:
      level: WARN             # [默认:WARN] 可选: SILENT/ERROR/WARN/INFO
      slow_threshold: 200ms   # 慢查询阈值
      colorful: true          # [默认:false] 彩色输出
      ignore_record_not_found_error: false  # [默认:false]
      parameterized_queries: true  # [默认:false] 参数化查询日志
  tracing:
    disable: false            # [默认:false]
    exclude_query_vars: false # [默认:false] 排除 SQL 变量
    exclude_metrics: false    # [默认:false] 排除 DBStats 指标
    record_stack_trace_in_span: false  # [默认:false] 异常包含堆栈
```

</details>

<details>
<summary><b>Redis 组件</b></summary>

```yaml
redis:
  default: primary            # 默认连接名
  log:
    level: info
  connections:
    primary:
      network: tcp            # [默认:tcp] 可选: tcp/unix
      addr: localhost:6379    # 必填
      client_name: my_app     # CLIENT SETNAME
      protocol: 3             # [默认:3] RESP 协议版本: 2/3
      username: ""            # 用户名
      password: your_password # 密码
      db: 0                   # [默认:0] 数据库编号
      max_retries: 3          # [默认:3] 最大重试次数，-1 禁用
      min_retry_backoff: 8ms  # [默认:8ms] 最小重试退避
      max_retry_backoff: 512ms  # [默认:512ms] 最大重试退避
      dial_timeout: 5s        # [默认:5s] 拨号超时
      read_timeout: 3s        # [默认:3s] 读超时，-1 无限制
      write_timeout: 3s       # [默认:3s] 写超时，-1 无限制
      pool_size: 10           # [默认:10*GOMAXPROCS] 连接池大小
      min_idle_conns: 5       # [默认:0] 最小空闲连接
      max_idle_conns: 10      # [默认:0] 最大空闲连接
      max_active_conns: 20    # [默认:0(无限制)] 最大活跃连接
      conn_max_idle_time: 30m # [默认:30m] 连接最大空闲时间
      conn_max_lifetime: 1h   # [默认:0(无限制)] 连接最大生命周期
      context_timeout_enabled: true  # [默认:false] 启用 context 超时
  tracing:
    disable: false            # [默认:false]
    db_statement: true        # [默认:false] 记录 redis 命令
    caller_enabled: true      # [默认:false] 记录调用位置
  metrics:
    disable: false            # [默认:false]
```

</details>

<details>
<summary><b>Job 组件</b></summary>

```yaml
job:
  disable: false              # [默认:false]
  timezone: Asia/Shanghai     # [默认:当前时区] 时区
  log:
    level: info
  jobs:
    sync_data:                # 任务名称（代码中注册使用）
      disable: false          # [默认:false]
      schedule: "0 */6 * * *" # 定时表达式（每6小时）
      concurrent_policy: SKIP # [默认:OVERLAP] 可选: OVERLAP/DELAY/SKIP
      immediately: false      # [默认:false] 启动时立即执行一次
    quick_check:
      schedule: "@every 30s"  # 每30秒
      concurrent_policy: SKIP
      immediately: true
  tracing:
    disable: false            # [默认:false]
    tracer_name: job_service  # [默认:继承 tracing 组件]
  metrics:
    disable: false            # [默认:false]
    meter_name: job_metrics   # [默认:继承 metrics 组件]
```

**Schedule 表达式：**

- Cron: `[Sec] Min Hour Day Month Week`（6段支持秒，5段不支持）
- 时区: `TZ=Asia/Tokyo 0 * * * *`
- 预设: `@yearly` `@monthly` `@weekly` `@daily` `@hourly`
- 间隔: `@every 1s` `@every 1m30s`

**并发策略：**

- `OVERLAP` - 允许重复执行（默认）
- `DELAY` - 等待上一个任务完成后执行
- `SKIP` - 跳过本次执行

</details>

## JSON Schema

支持 IDE 自动补全和校验：

```yaml
# $schema: https://raw.githubusercontent.com/jaggerzhuang1994/kratos-foundation/main/config.schema.json
log:
  level: info
# ...
```

**VS Code 配置：**

```json
{
  "yaml.schemas": {
    "https://raw.githubusercontent.com/jaggerzhuang1994/kratos-foundation/main/config.schema.json": [
      "config*.yaml"
    ]
  }
}
```

## 依赖关系

```
┌─────────────────────────────────────────┐
│              app (应用层)                │
├─────────────────────────────────────────┤
│  server    client    job    database    │
│                     redis               │
├─────────────────────────────────────────┤
│     metrics      tracing      log       │
├─────────────────────────────────────────┤
│        config        consul             │
└─────────────────────────────────────────┘
```

## License

MIT
