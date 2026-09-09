# appinfo

`appinfo` 在应用启动时生成不可变的进程身份，供日志、Tracing、Metrics 和服务注册复用。

```go
info := appinfo.New(version)
```

业务和 Wire 只依赖公开的 `AppInfo` 与 `New`。主机名、可执行文件名及其回退逻辑位于同包的 `appinfo.go`，通过非导出标识符保留启动时快照。

组装时，`bootstrap.NewAppInfoBootstrap(info, spec, shared)` 会同步向 `app.Spec` 登记唯一 AppInfo，并向 `log.SharedState` 添加 `service.id`、`service.name` 与 `service.version` 字段：

```go
contribution, err := bootstrap.NewAppInfoBootstrap(info, appSpec, sharedLogState)
```

该 Bootstrap 不创建资源也不返回 cleanup；进程身份由 `New` 创建后一直由调用方/Wire 持有。
