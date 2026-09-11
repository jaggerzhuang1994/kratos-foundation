# 本地组件指标实测

2026-09-11 本地 Docker 执行五轮真实业务，59 项精确增量检查全部通过。完整原始报告由 `make -C examples/components exercise` 写入 `.runtime/latest-report.json`（不提交运行数据）。

| 检查 | 预期 | 实际 |
| --- | --- | --- |
| `business_demo_runs_total`  | 5 | 5 |
| `database_sql_operations_total` db_name=primary, operation=create, result=success | 5 | 5 |
| `database_sql_operations_total` db_name=primary, operation=query, result=success | 10 | 10 |
| `database_sql_operations_total` db_name=primary, operation=update, result=success | 5 | 5 |
| `database_sql_operations_total` db_name=primary, operation=delete, result=success | 5 | 5 |
| `database_sql_operations_total` db_name=primary, operation=query, result=not_found | 5 | 5 |
| `database_sql_operations_total` db_name=primary, operation=raw, result=success | 5 | 5 |
| `business_cache_lookups_total` cache_name=orders, result=hit | 15 | 15 |
| `business_cache_lookups_total` cache_name=orders, result=miss | 5 | 5 |
| `business_cache_loads_total` cache_name=orders, result=success | 5 | 5 |
| `lock_operations_total` lock_name=orders, operation=lock, result=success | 5 | 5 |
| `lock_operations_total` lock_name=orders, operation=try_lock, result=contended | 5 | 5 |
| `lock_operations_total` lock_name=orders, operation=ttl, result=success | 5 | 5 |
| `lock_operations_total` lock_name=orders, operation=refresh, result=success | 5 | 5 |
| `lock_operations_total` lock_name=orders, operation=unlock, result=success | 5 | 5 |
| `lock_released_hold_duration_seconds_count` lock_name=orders | 5 | 5 |
| `oss_requests_total` bucket=assets, operation=put, result=success | 5 | 5 |
| `oss_request_duration_seconds_count` bucket=assets, operation=put, result=success | 5 | 5 |
| `oss_requests_total` bucket=assets, operation=stat, result=success | 5 | 5 |
| `oss_request_duration_seconds_count` bucket=assets, operation=stat, result=success | 5 | 5 |
| `oss_requests_total` bucket=assets, operation=exists, result=success | 5 | 5 |
| `oss_request_duration_seconds_count` bucket=assets, operation=exists, result=success | 5 | 5 |
| `oss_requests_total` bucket=assets, operation=get, result=success | 5 | 5 |
| `oss_request_duration_seconds_count` bucket=assets, operation=get, result=success | 5 | 5 |
| `oss_requests_total` bucket=assets, operation=delete, result=success | 5 | 5 |
| `oss_request_duration_seconds_count` bucket=assets, operation=delete, result=success | 5 | 5 |
| `oss_transferred_bytes_total` bucket=assets, operation=put | 220 | 220 |
| `oss_transferred_bytes_total` bucket=assets, operation=get | 220 | 220 |
| `oss_streams_total` bucket=assets, result=success | 5 | 5 |
| `queue_producer_messages_total` queue_destination=components-tasks, queue_result=success | 15 | 15 |
| `queue_consumer_attempts_total` queue_destination=components-tasks, queue_result=success | 10 | 10 |
| `queue_consumer_attempts_total` queue_destination=components-tasks, queue_result=error | 10 | 10 |
| `queue_consumer_retries_total` queue_destination=components-tasks | 5 | 5 |
| `queue_consumer_messages_total` queue_destination=components-tasks, queue_result=success | 10 | 10 |
| `kafka_producer_messages_total` kafka_destination=components-events, kafka_result=success | 15 | 15 |
| `kafka_consumer_attempts_total` kafka_destination=components-events, kafka_result=success | 10 | 10 |
| `kafka_consumer_attempts_total` kafka_destination=components-events, kafka_result=error | 10 | 10 |
| `kafka_consumer_retries_total` kafka_destination=components-events | 5 | 5 |
| `kafka_consumer_messages_total` kafka_destination=components-events, kafka_result=success | 10 | 10 |
| `queue_consumer_messages_total` queue_destination=components-tasks, queue_result=failed | 5 | 5 |
| `queue_consumer_failed_tasks_total` queue_destination=components-tasks | 5 | 5 |
| `kafka_consumer_messages_total` kafka_destination=components-events, kafka_result=dead_lettered | 5 | 5 |
| `kafka_consumer_dead_letters_total` kafka_destination=components-events | 5 | 5 |
| `kafka_producer_messages_total` kafka_destination=components-deadletter, kafka_result=success | 5 | 5 |
| `queue_tasks` queue_destination=components-backlog, state=ready | 5 | 5 |
| `queue_tasks` queue_destination=components-backlog, state=scheduled | 5 | 5 |
| `queue_tasks` queue_destination=components-tasks, state=failed | 5 | 5 |
| `client_requests_code_total` operation=/example.Greeting/Hello, code=200 | 5 | 5 |
| `server_requests_code_total` operation=/example.Components/Run, code=200 | 5 | 5 |
| `server_requests_code_total` operation=/example.Components/Catalog, code=200 | 5 | 5 |
| `client_requests_code_total` operation=/example.Components/Catalog, code=200 | 5 | 5 |
| `server_requests_code_total` operation=/example.Components/Inventory, code=200 | 5 | 5 |
| `client_requests_code_total` operation=/example.Components/Inventory, code=200 | 5 | 5 |
| `queue_producer_messages_total` queue_destination=components-email, queue_result=success | 5 | 5 |
| `queue_consumer_messages_total` queue_destination=components-email, queue_consumer=email-worker, queue_result=success | 5 | 5 |
| `queue_consumer_attempts_total` queue_destination=components-email, queue_consumer=email-worker, queue_result=success | 5 | 5 |
| `queue_producer_messages_total` queue_destination=components-report, queue_result=success | 5 | 5 |
| `queue_consumer_messages_total` queue_destination=components-report, queue_consumer=report-worker, queue_result=success | 5 | 5 |
| `queue_consumer_attempts_total` queue_destination=components-report, queue_consumer=report-worker, queue_result=success | 5 | 5 |

## 时延和状态

以下是本轮真实采样的直方图估算 P95 与 sum/count 均值，不是性能基准。SQL 加入有意较慢的递归汇总，每轮一次。

| 指标 | 均值（ms） | P95（ms） |
| --- | --- | --- |
| `database_sql_operation_duration_seconds` | 13.1795 | 145.0000 |
| `oss_request_duration_seconds` | 0.7134 | 0.9429 |
| `lock_operation_duration_seconds` | 0.0492 | 0.1000 |
| `queue_consumer_attempt_duration_seconds` | 0.0438 | 0.1763 |
| `kafka_consumer_attempt_duration_seconds` | 0.0488 | 0.2200 |

慢 SQL 阈值实测 10ms，raw 慢操作增量 5，预期至少 5。Server、Client、Job、Queue 分别有三组可区分的接口/任务/消费者序列。

healthz/readyz、四个 Queue 统计采集状态和年龄可用性均为 1；Kafka lag=0；业务缓存命中率 75%。

配置热更新实际结果：`{"accepted_delta": 4.0, "rejected_delta": 4.0, "pool_max_applied": 9, "pool_max_restored": 8}`。一次替换可能产生多个文件通知，因此不要求 accepted/rejected 精确 +1。

## 验证与边界

- `make -C examples/components test`（race + vet）、`up`、`exercise` 通过。构造签名未变，本轮没有需要重新生成的 Wire 变更。
- Database/Metrics 包 race 与 vet 通过；包括慢 SQL 边界、显式零值禁用和阈值覆盖回归。
- `make -C deploy/observability check`（20 条规则及用例）、`smoke`（705 个 PromQL 查询）、`render-kubernetes` 通过；两份 Dashboard 均已由 Grafana 加载，默认展开且面板位置无重叠。
- 文档核对包括组件示例、指标说明、Dashboard 与普通/Kubernetes 部署；更新流程图并检查本地链接和两份渲染 JSON。
- 本轮没有执行全仓 make verify；局部通过不代表并行修改中的整个工作区通过。
- SQLite、Redis、Kafka 为真实后端；OSS 是真实 SDK 访问本地协议模拟服务，未验证阿里云/MySQL；未部署 Kubernetes，未启用宿主机 node-exporter。

流程与资源边界见 [组件示例](README.md)。
