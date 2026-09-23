# Consul 进程单例

配置源驱动和注册/发现驱动统一调用 `Get()`，借用同一个 SDK 客户端。业务只导入 contrib 驱动，不直接导入本 internal 包；旧 pkg/consul 构造入口已删除。

第一次调用读取环境（Consul SDK 的 `CONSUL_HTTP_ADDR`、`CONSUL_HTTP_TOKEN`、TLS 等变量），构造客户端并在 10 秒内探测 leader。`DISABLE_CONSUL=true`，或 local 环境没有设置地址，表示禁用，`Get()` 返回 `nil, true, nil`。真实初始化失败返回 `nil, false, err`；成功返回 `client, false, nil`。配置源在禁用时跳过，注册实例不执行注册；要求服务发现的客户端仍校验发现能力是否可用。

首次结果由 `sync.Once` 固定，包括初始化错误和禁用结果；以后 env 变化不生效，不自动重试。成功的客户端归进程所有，不提供调用方 cleanup 或 Reset。驱动仅停止自己的 watcher、心跳等任务。初始化过程不返回 cleanup，也不单独管理连接池释放；首次失败被缓存，不循环创建客户端，连接资源由进程生命周期及底层 transport 管理。

多个具名 Consul 实例共享同一连接与集群；各自保留注册及发现策略。SDK 客户端只供借用，调用方不得修改客户端配置、令牌或关闭 transport。单例持有 SDK 并发安全客户端，不串行化后续请求。

```mermaid
flowchart TD
 A([配置源或注册发现并发调用 Get]) --> B[进入 sync.Once.Do]
 B --> C{首次调用?}
 C -- 否 --> D[等待首次调用结束，读取已发布结果]
 C -- 是 --> E[读取 env 固定启用决策及连接配置]
 E --> F{已禁用?}
 F -- 是 --> G[WARN consul client is disabled；缓存 disabled=true]
 F -- 否 --> H[INFO 初始化；构造客户端并探测 leader，限时10秒]
 H --> I{成功?}
 I -- 否 --> J[缓存初始化错误，不再创建客户端]
 I -- 是 --> K[缓存客户端，所有权归进程]
 G --> L[Once 完成并发布结果]
 J --> L
 K --> L
 L --> D
 D --> M{缓存错误?}
 M -- 是 --> N([返回错误，由启动边界处理])
 M -- 否 --> Z{disabled?}
 Z -- 是 --> V([返回 nil,true,nil；由驱动跳过])
 Z -- 否 --> O[返回同一客户端；请求并发执行]
 O --> P[驱动 cleanup 只停止自有任务]
 P --> Q([客户端保留至进程退出])
```

同步范围仅包括首次初始化及结果发布。并发首次调用最多等待一次 10 秒探测；没有驱动级锁，也没有失败重试循环。测试使用本机 HTTP 服务验证并发单例、环境冻结、失败缓存、域名与 env token 传递。
