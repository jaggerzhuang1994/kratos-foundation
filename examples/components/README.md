# 组件示例

在一个应用中组合使用数据库、Redis、分布式锁、OSS、Kafka、持久化队列、定时任务和 HTTP 客户端，展示各组件的构造、资源所有权与登记方式。代码随仓库编译和测试；在本地 Docker 中运行并核对真实指标见 [test/components](../../test/components/README.md)。

不需要全部组件的项目从 [最小示例](../minimal/README.md) 开始，按需复制本例的 provider 与业务代码。

| 文件 | 演示内容 |
| --- | --- |
| [cmd/api/bootstrap.go](cmd/api/bootstrap.go) | 登记 HTTP 路由、Redis 就绪检查 `spec.Health().Checks`、三个 Cron 任务、Kafka 消费者与队列 Worker |
| [cmd/api/data.go](cmd/api/data.go) | `database.Manager.Transaction` 事务、缓存回源（Redis）、业务缓存指标、`lock` 租约获取/续租/释放 |
| [cmd/api/storage.go](cmd/api/storage.go) | OSS bucket 上传、Stat、下载校验与删除，读完后关闭流 |
| [cmd/api/messaging.go](cmd/api/messaging.go) | Kafka Producer（含 ManagedProducer）、ConsumerRuntime 重试与死信；`queue.Queue[T]` 投递与 `queue.Worker[T]` 消费；队列统计指标 `queue.RegisterStats` |
| [cmd/api/service.go](cmd/api/service.go) | HTTP 端点、通过 `client.Factory.AcquireClient` 调用其他服务并 `release` |
| [cmd/api/wire.go](cmd/api/wire.go) | 组合以上 provider 及其 cleanup |
| [configs/config.yaml](configs/config.yaml) | 各组件的配置示例（地址为本地 Docker 环境） |

## 要点

- **资源所有权**：Manager / Factory 拥有连接，业务只借用，不要自行 `Close`；`messaging` 独占的 Producer 与统计注册由它自己的 cleanup 释放，且在应用停止所有运行时之后执行。
- **运行时**：Kafka 消费者通过 `spec.RegisterKafkaConsumer`、队列 Worker 通过 `spec.RegisterRuntime` 登记，启动与停止由应用统一负责，业务不启动 goroutine。
- **幂等**：Kafka 与队列都是至少一次处理；示例用 Redis `INCR+EXPIRE` 的 Lua 原子计数模拟“首次失败、重试成功”，业务应按自身数据设计幂等。
- **事务边界**：数据库事务不覆盖 Redis、OSS、Kafka；示例失败时有界清理已写入的资源，不提供跨组件事务，不能把重放整次请求当作补偿。
- **日志**：业务日志统一使用注入的 Logger，按模块区分（`demo`、`data`、`messaging`、`maintenance`），逐条成功为 DEBUG，阶段汇总为 INFO，处理边界失败为 ERROR。
- **指标**：请求、数据库、Redis、Kafka、队列、锁、OSS、Job 的指标由各组件自动产生，业务只补充低基数的业务指标；面板见 [observability](../../observability/README.md)。
