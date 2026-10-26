# 最小示例

只依赖 Foundation 公共包的最小 HTTP 应用：文件配置、日志、指标、健康检查、Wire 组装和一个业务端点。代码随仓库一起编译和测试，保证示例与当前 API 一致；运行环境见 [test/observability](../../test/README.md)。

| 文件 | 演示内容 |
| --- | --- |
| [cmd/api/main.go](cmd/api/main.go) | 解析参数、记录 `command.starting`、调用 Wire injector、`application.Run()` 与逆序 cleanup |
| [cmd/api/wire.go](cmd/api/wire.go) | 显式列出各 provider 的 injector（等价于 `bootstrap.BaseProviderSet` 的子集），便于理解组装顺序 |
| [cmd/api/bootstrap.go](cmd/api/bootstrap.go) | `newSpec` 声明文件配置源；`boot` 用 `spec.Http().Register` 登记路由 |
| [cmd/api/service.go](cmd/api/service.go) | 手写 Kratos HTTP 路由：设置稳定 operation、显式调用 `ctx.Middleware`，以及一个低基数业务计数器 |
| [configs/config.yaml](configs/config.yaml) | 业务端口 8000、管理端口 9001（`/healthz` `/readyz` `/metrics`）、关闭 gRPC |

## 要点

- 业务只借用注入的 Logger、指标 Provider；资源 cleanup 由 Wire 持有，`Run` 返回后才执行。
- 手写路由必须调用 `ctx.Middleware`，否则不会经过默认中间件，也不会产生请求指标；生成的 HTTP 服务不需要。
- 业务指标标签保持低基数，不使用用户 ID、订单 ID、请求 ID。
- 应用身份 `AppInfo.Name()` 默认取可执行文件名，应与 Prometheus 的 `app` 标签保持一致。
- 示例导入了 Consul 注册驱动；本地未设置 `CONSUL_HTTP_ADDR` 时驱动自动禁用，也可设置 `DISABLE_CONSUL=true`。

## 复制为业务项目

1. 复制 `cmd/api` 与 `configs` 到业务仓库，把默认配置路径改为业务自己的 `configs/config.yaml`。
2. 固定 Foundation 版本并生成 Wire：

   ```sh
   go get github.com/jaggerzhuang1994/kratos-foundation/v2@<tag 或 commit>
   go get -tool github.com/google/wire/cmd/wire@v0.7.0
   go tool wire ./cmd/api
   ```

3. 业务代码放入自己的 `internal/`，在 `boot` 中登记服务，在 `wire.go` 中注入依赖；需要数据库、Redis、队列等组件时，改用 `bootstrap.BaseProviderSet`，见根 [README](../../README.md#快速开始) 与 [components 示例](../components/README.md)。
