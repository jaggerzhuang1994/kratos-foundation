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
| `order-api` | api | 下单：分布式锁、调用库存、MySQL 事务（3% 慢 SQL）、Redis 缓存、投递队列与 Kafka；查单：缓存回源 |
| `inventory-api` | inventory | 下游库存服务：5–80ms 延迟，2% 慢请求，5% 返回 503 |
| `order-worker` | worker | Redis 队列 Worker 与 Kafka ConsumerRuntime：85% 成功、10% 首次失败后重试成功、5% 永久失败进死信/失败归档 |
| `order-cron` | cron | 4 个 Cron：对账、清理（20% 失败）、3–8 秒结算、向无 Worker 的延迟队列投递（积压） |
| `stats-daemon` | daemon | 自定义 Runtime 与 Daemon 任务；每 5 分钟前 40 秒就绪检查失败 |
| `loadgen` | loadgen | 约 15 rps 访问网关 |
| `shop-report` | report | K8s CronJob 每 2 分钟一次的一次性报表，约 30% 失败 |

同一镜像通过 `APP_NAME` 取 Pod 标签 `app.kubernetes.io/name` 作为应用名，日志、注册名与指标的 `app` 一致。

代码位于 [cmd/demo](cmd/demo)，带 `demo` 构建标签，不参与 `go build ./...`、`make test` 与 lint。修改 `wire.go` 后执行 `go tool wire gen -tags demo ./test/demo/cmd/demo`，并把生成文件的构建约束改为 `//go:build demo && !wireinject`。
