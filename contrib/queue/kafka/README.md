# Kafka 队列适配器

`NewProducer` 创建并独占 Kafka 客户端，返回幂等 cleanup；调用方在停止使用 Producer 后释放它。`NewConsumer` 借用 `pkg/kafka.ClientFactory`，每次 Consume 按并发数创建客户端并在退出时关闭；整批消息处理成功后才提交位点。

实现保持在同一个包内，按职责组织：

- `driver.go`：公共构造入口与客户端选项。
- `config.go`：配置校验与默认值。
- `producer.go`：发布、批量失败收集与 Producer cleanup。
- `consumer.go`：消费者生命周期、并发实例、暂时故障恢复与客户端关闭。
- `consumer_recovery.go`：Kafka 操作错误与可恢复失败分类。
- `consumer_fetch.go`：Fetch 事件分类、投递和位点提交。
- `message.go`：通用消息与 Kafka Record 的双向转换。

与业务 Handler、可观测性及应用生命周期的组装方式见 [queue 使用说明](../../../pkg/queue/README.md)。

## 断线与消费恢复

franz-go 自身负责连接重建、Fetch 重试和消费组会话恢复，保持其原生指数退避。若这些机制之外的 Kafka 操作仍返回暂时错误（例如提交位点耗尽 SDK 重试或组代次失效），仅故障 worker 关闭旧客户端，再通过可取消退避重新创建客户端；其他 worker 继续运行。额外恢复等待从 100ms 基数翻倍至 5s，加入 80%–100% 抖动；一旦成功处理并提交消息，连续失败次数重置。

重建后优先从 Broker 的已提交位点继续消费。首次会话尊重配置的 `StartLatest`；恢复会话若仍没有已提交位点，则从该分区最早保留记录开始，可能重放保留历史，以避免首批提交失败后跳过已投递记录。已存在的提交位点和配置的越界重置策略保持不变。提交结果不确定或提交失败时，已执行的 Handler 可能再次执行，因此仍要求业务幂等，保持 **at-least-once** 语义。恢复不会直接重试旧客户端的业务 Handler，也不会跨越未确认批次提交新位点。

只有明确发生在 Kafka 构造、Fetch 或提交阶段的暂时错误才触发恢复。Handler 即使返回 EOF 等网络错误，也会照常终止消费；认证、授权、客户端关闭及其他永久错误同样终止。worker Context 控制 Poll 和退避等待，每轮 SDK 客户端拥有独立的生命周期。退出时先允许再均衡，再用最多 5 秒的独立 Context 执行 LeaveGroup，最后取消 SDK Context 并关闭客户端。健康 Broker 可立即释放成员及分区；退组超时或失败会记录 WARN，并继续清理，Broker 侧成员仍可能保留至会话超时。

```mermaid
flowchart LR
    A[worker 创建本轮客户端] --> B[Poll 与业务处理]
    B -- 整批成功 --> C[提交位点]
    C -- 成功 --> B
    B -- 取消或业务及永久错误 --> D[允许再均衡 最多等待 5s 退组]
    D -- 退组失败或超时 --> H[WARN leave group failed]
    D -- 退组成功 --> I[取消 SDK Context 关闭并返回错误]
    H --> I
    B -- 暂时 Kafka 操作失败 --> E[允许再均衡 最多等待 5s 退组]
    C -- 暂时提交失败 --> E
    E -- 退组失败或超时 --> J[WARN leave group failed]
    E -- 退组成功 --> K[取消 SDK Context 关闭旧客户端]
    J --> K
    K --> F[WARN 并可取消指数退避]
    F --> G[优先已提交位点 缺失时从最早保留记录恢复]
    G --> A
    A -- 暂时构造错误 --> F
```
