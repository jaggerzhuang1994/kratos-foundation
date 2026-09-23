# 阿里云 OSS 适配器

本包实现 `pkg/oss.Bucket`，导入后注册 `aliyun` 驱动。业务 Wire 层构造 `oss.NewManager`，通过逻辑 bucket 名借用实例；Manager 持有资源并负责 cleanup。也可显式调用 `New(Config)` 创建独立实例。

`GetObject` 保留响应中的 `ContentEncoding`、`CacheControl` 和 `ContentDisposition`，与对象其他元信息一起返回。未提供的响应头对应空字符串；调用方负责关闭返回的 Body。

实现保留在同一个包内：

- `bucket.go`：驱动注册、SDK 配置、Bucket 构造与对象 URL。
- `object.go`：对象操作及其输入校验、Range、对象信息和错误转换。

```mermaid
flowchart LR
    A([业务 Wire 组装]) --> B[导入本包注册 aliyun 驱动]
    B --> L[INFO registered OSS driver: driver=aliyun]
    L --> C[oss.NewManager 固定驱动与配置]
    C --> D[按逻辑名称取得 Bucket]
    D --> E{配置和构造成功?}
    E -- 否 --> F[返回错误给调用方]
    E -- 是 --> G[执行对象操作]
    G --> H{操作成功?}
    H -- 否 --> I[转换错误并返回调用方]
    H -- 是 --> J[返回对象或操作结果]
    F --> K([结束])
    I --> K
    J --> K
```

具体配置、资源所有权及业务调用方式见 [oss 使用说明](../../../pkg/oss/README.md)。
