# OSS

`pkg/oss` 提供业务与 Wire 组装层使用的对象存储 Manager、通用 Bucket 契约、域名辅助类型和驱动注册入口；云厂商实现放在 `contrib/oss/<driver>`。

公共契约与实现集中在 `pkg/oss`：`driver.go` 管理驱动注册，`manager.go` 校验 Bucket 定义并管理延迟缓存和关闭状态，`domain.go` 处理域名解析。`NewManager` 取得一次冻结的驱动快照，非导出的 `manager` 持有实例和资源。

## 注册 Aliyun 驱动

应用在组装层空导入需要的驱动：

```go
import _ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/oss/aliyun"
```

空导入触发的 `init()` 只向 `pkg/oss` 注册无状态工厂，不会访问远程对象存储。Bucket 在业务首次调用 `Manager.Bucket` 时延迟创建。

OSS 配置必须显式填写已注册的驱动名；仓库提供的 Aliyun 实现使用 `aliyun`：

```yaml
oss:
  buckets:
    assets:
      driver: aliyun
      bucket: production-assets
      domain: https://static.example.com
      options:
        region: cn-hangzhou
        access_key_id: ${ALIYUN_ACCESS_KEY_ID}
        access_key_secret: ${ALIYUN_ACCESS_KEY_SECRET}
```

同一个 Manager 可以让不同逻辑 Bucket 使用不同的已注册驱动。新增七牛云等实现时，应在独立公共 `contrib/oss/<driver>` 包中注册工厂，由业务组装层选择性空导入。

## 注册时序

首个 `oss.NewManager` 会冻结全局驱动注册表并持有不可变快照。所有驱动包都必须在此之前导入；冻结后调用 `RegisterDriver` 或 `MustRegisterDriver` 会失败。驱动工厂的执行不持有注册表锁。

## 构造与释放

构造入口为 `oss.NewManager(configManager config.Manager, logger log.Logger) (Manager, func(), error)`。在 Wire 中直接提供该构造函数；手工构造时使用返回的 cleanup 释放资源。

```go
manager, cleanup, err := oss.NewManager(configManager, logger)
if err != nil {
    return err
}
defer cleanup()
bucket, err := manager.Bucket("assets")
```

Manager 持有其创建的 Bucket，cleanup 幂等关闭其中实现 `io.Closer` 的实例。关闭失败通过注入的 logger 记录 `oss` 模块日志，业务不单独关闭借用的 Bucket。

```mermaid
flowchart TD
    A([NewManager]) --> B[读取配置并冻结驱动快照]
    B -- 校验失败 --> C[返回错误]
    B -- 成功 --> D[业务调用 Bucket]
    D --> E{已缓存?}
    E -- 否 --> F[调用驱动工厂并接管实例]
    F -- 失败 --> C
    E -- 是 --> G[返回借用的 Bucket]
    F -- 成功 --> G
    G --> H[Wire cleanup 关闭已创建实例]
    H -- 关闭失败 --> I[实例 logger ERROR cleanup.failed]
    H -- 成功 --> J([结束])
    I --> J
    C --> J
```

## 对象键与公开 URL

`BucketDomainHelper` 接收原始相对对象键或属于配置域名、基础路径的绝对 URL。相对键中的 `%`、`%2F`、`?` 和 `#` 都是字面字符，例如 `reports/100%.pdf` 生成 `reports/100%25.pdf`，`reports/%2F.pdf` 生成 `reports/%252F.pdf`。绝对 URL 按 URL 规则解码，路径中的 `%2F` 恢复为 `/`；域名、scheme 或基础路径不匹配、非法转义、userinfo、查询和片段会返回错误。

```mermaid
flowchart TD
    A([输入对象键或 URL]) --> B{首个路径、查询或片段分隔符前有冒号?}
    B -- 否 --> C[保留相对键字面字符 去掉前导斜杠]
    B -- 是 --> D[解析绝对 URL 并解码路径]
    D -- 解析失败或域名、基础路径不匹配 --> E([返回错误])
    D -- 含 userinfo、查询或片段 --> E
    D -- 校验通过 --> F[从解码路径提取对象键]
    C --> G{GetFullURL?}
    F --> G
    G -- 否 --> H([返回对象键])
    G -- 是且键为空 --> E
    G -- 是且键非空 --> I[拼接配置基础路径并按 URL 规则转义]
    I --> J([返回公开 URL])
```
