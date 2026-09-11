# 健康状态与 Kafka Lag 的外部采集

## Ready 与 Health

`up` 表示 Prometheus 是否取得一次合法指标响应，不能表示应用就绪。模板用 [blackbox-exporter](https://github.com/prometheus/blackbox_exporter) 实际 GET 每个实例的 `/readyz` 和 `/healthz`，仅 HTTP 200 成功，不跟随重定向，单次探测超时2s、抓取超时5s。

本地 `make -C deploy/observability up` 已包含 blackbox 容器，不向宿主机发布其端口。普通部署需要运行同一 [blackbox 配置](../prometheus/blackbox.yaml)，并在 [standalone.yaml](../prometheus/standalone.yaml) 修改应用地址、blackbox地址与身份。探测 `instance` 去掉路径后与应用 `/metrics` 地址相同；`foundation_probe=true` 与应用 `foundation=true` 分开，避免多算应用实例数。

Kubernetes 可复用既有 blackbox，或审阅 [blackbox.yaml](../../kubernetes/blackbox.yaml) 后部署；将 [health-values.yaml](../../kubernetes/health-values.yaml) 的两个 additionalScrapeConfigs 合并到现有 kube-prometheus-stack。示例只发现 foundation-demo 的 minimal-api Service management 端口，探测各 Pod 地址；按实际修改 namespace、服务标签、端口和集群名。Prometheus 需有发现 Endpoints/Pod/Service 的权限，blackbox 需能访问管理端口；探测接口不能暴露给不可信网络任意访问内部地址。

面板将 ready、healthz、探测采集状态分开；按 App 聚合时取最差值，有一个实例失败就显示0。探测器故障时 `up=0`，不会伪造应用健康。应用健康不等于完整外部用户链路健康；readiness依赖哪些组件取决于业务注册的 HealthConfig.Checks。

```mermaid
flowchart TD
    A([Prometheus 发现每个应用实例]) --> B[携带目标URL请求 blackbox]
    B --> C{blackbox 可访问?}
    C -- 否 --> D([up为0 触发探测采集告警])
    C -- 是 --> E[有界GET readyz或healthz]
    E --> F{HTTP200且未超时?}
    F -- 否 --> G[probe_success为0]
    F -- 是 --> H[probe_success为1]
    G --> I[附加app node pod instance target身份]
    H --> I
    I --> J([面板分别展示就绪 存活和采集状态])
```

## Kafka Lag

Lag 是消费组在共享 Kafka 中的位点差，不能从 handler 执行计数或某个 Pod 的吞吐推算。模板接入 [kafka-exporter](https://github.com/danielqsj/kafka_exporter) 的 `kafka_consumergroup_lag`，提供分区和 Topic 合计；负值标为未知并排除合计，多副本 exporter 对同一分区使用max去重。

仓库根目录启动可选本地 exporter（连接已有 Kafka；broker 与返回的 advertised.listeners 均须可从容器访问）：

```sh
KAFKA_BROKER=host.docker.internal:19092 KAFKA_VERSION=4.1.2 \
  docker compose -f deploy/observability/compose.yaml \
  -f deploy/observability/compose.kafka.yaml up -d kafka-exporter prometheus
```

上述版本是示例，请匹配实际 broker；可设置 `KAFKA_TOPIC_FILTER`、`KAFKA_GROUP_FILTER` 缩小范围。启动会把 [示例文件发现目标](../prometheus/kafka-targets.example.json) 挂载为 kafka-targets.json，普通配置默认文件为空。普通非Compose部署修改 kafka-targets.json为实际地址，env/cluster与顶层筛选一致，kafka_cluster是稳定共享集群名。不要把 exporter 实例名或业务 app 当作 Kafka 集群名。

Kubernetes 复用已有 exporter 时只对齐 foundation_kafka/env/cluster/kafka_cluster 标签；否则使用 [kafka-exporter.yaml](../../kubernetes/kafka-exporter.yaml) 示例，先修改 broker、版本、Topic/Group过滤、release标签及认证。TLS/SASL参数和证书按部署环境配置，密码通过部署系统Secret管理，不写入仓库。本例启用offset.show-all以包括未连接消费组；结果仍受权限、组/Topic过滤和已提交位点是否存在影响。Exporter读取元数据与消费组位点，不作为业务消费者，不创建Topic。

Lag分区只受 env、cluster、kafka_cluster、lag_group、lag_topic 筛选；不受 App/Node/Pod 筛选，因为没有可靠的一对一归属。若业务确需按App过滤，需自己维护消费组到App的明确映射，不能按名字猜测。此例没有部署生产Kafka，也不验证业务SASL/ACL；请检查Exporter日志与原生命令的已提交位点后再使用告警阈值。

```mermaid
flowchart TD
    A([启用可选Kafka exporter]) --> B[读取固定broker与Topic/Group过滤]
    B --> C[有权限地查询Kafka元数据与消费组位点]
    C --> D{查询成功且有已提交位点?}
    D -- 否 --> E([检查认证 ACL 广播地址 过滤和位点状态])
    D -- 是 --> F[输出每个Group Topic Partition Lag]
    F --> G[Prometheus附加部署与Kafka集群身份]
    G --> H[按分区去重 再求Topic总Lag]
    H --> I([共享集群面板与业务阈值告警])
```
