# 本地 Kubernetes 监控 Demo

在 kind 集群中用框架跑一组真实业务角色，产生指标，验证 [observability](../../observability/README.md) 的面板、recording rules 与告警。

```sh
test/demo/run.sh up      # 创建集群并部署全部组件（首次约 15–20 分钟）
test/demo/run.sh reload  # 只重新构建并部署应用、面板与规则
test/demo/run.sh down    # 删除集群
```

- Grafana：<http://127.0.0.1:13300>（匿名只读；管理员 `admin` / `demo-local-only`），Prometheus：<http://127.0.0.1:19900>
- kind、helm 由脚本安装到仓库 `tools/`；宿主机代理若为 `127.0.0.1`，节点内自动改写为 `host.docker.internal`。
- 需要 Docker、约 6–8GB 可用内存。

| 应用 | 角色 | 演示内容 |
| --- | --- | --- |
| `gateway` | gateway | HTTP 入口，通过 `client.Factory` 转发到 order-api（网关详情的入口与下游） |
| `order-api` | api | 下单：分布式锁、调用库存（GET 失败重试）、MySQL 事务（3% 慢 SQL）内写订单与 Kafka 事务外盒、本 Pod 的外盒 Relay 发布、投递 Redis 队列；查单：`pkg/cache` 旁路缓存（并发回源合并、不存在订单缓存 30 秒），未命中从只读副本回源 |
| `inventory-api` | inventory | 下游库存服务：5–80ms 延迟，4% 慢于 1s（延迟 SLO），5% 返回 503（可用性 SLO），0.5% panic（被框架恢复，FoundationPanicRecovered） |
| `order-worker` | worker | Redis 队列 Worker（开启续租，2% 任务耗时 12 秒、租约 6 秒）与 Kafka ConsumerRuntime：83% 成功、10% 首次失败后重试成功、5% 永久失败进死信/失败归档 |
| `order-cron` | cron | 2 个副本、4 个 Cron：对账与清理带 Redis 集群锁，每次触发只有一个副本执行（另一副本记 `cluster_locked`）；清理 20% 失败、3–8 秒结算、向无 Worker 的延迟队列投递（积压） |
| `stats-daemon` | daemon | 自定义 Runtime 与 Daemon 任务；每 5 分钟前 40 秒就绪检查失败 |
| `loadgen` | loadgen | 约 15 rps 访问网关 |
| `shop-report` | report | K8s CronJob 每 2 分钟一次的一次性报表，约 30% 失败 |

所有应用在管理端口 9001 开启 `/debug/pprof/`（业务端口 8000 返回 404）；数据库连接配置了 `read_replicas`，副本 `mysql-replica` 与主库是同一个 MySQL 的不同 Service，用于演示读写路由与 `go_sql_*{db_name="primary/replica-1"}` 独立连接池指标，不演示真实复制延迟。

`test/demo/run.sh reject-config` 热下发一份非法客户端配置（重试次数超出范围），约 1 分钟内各应用拒绝更新并保留旧配置，触发 `client.config.rejected`、`log_events_total` 与 FoundationConfigRejected；`test/demo/run.sh restore-config` 恢复。

同一镜像通过 `APP_NAME` 取 Pod 标签 `app.kubernetes.io/name` 作为应用名，日志、注册名与指标的 `app` 一致。

代码位于 [cmd/demo](cmd/demo)，带 `demo` 构建标签，不参与 `go build ./...`、`make test` 与 lint。修改 `wire.go` 后执行 `go tool wire gen -tags demo ./test/demo/cmd/demo`，并把生成文件的构建约束改为 `//go:build demo && !wireinject`。
