# env

`env` 提供 Foundation 约定的应用环境判断，以及少量启动配置读取函数。它面向进程启动阶段；非法显式配置会
panic，以避免服务在错误环境下继续运行。

## 应用环境

```go
switch {
    case env.IsLocal():
    // 本地开发
    case env.IsProd():
    // 生产环境
}
```

`AppEnv` 按以下顺序读取环境变量：

1. `APP_ENV`
2. `KRATOS_ENV`
3. 均未设置时使用 `local`

允许值为 `local`、`dev`、`test`、`pre` 和 `prod`。显式设置空字符串、大小写错误、前后空白或其他值都会 panic。

这些值分别由公开常量 `Local`、`Dev`、`Test`、`Pre` 和 `Prod` 表示，调用方应使用常量比较，避免散落字符串字面量。

| API                                  | 含义                         |
|--------------------------------------|------------------------------|
| `AppEnv()`                           | 返回校验后的环境名           |
| `IsLocal()` / `IsDev()` / `IsTest()` | 判断本地、共享开发和测试环境 |
| `IsPre()` / `IsProd()`               | 判断预发布和生产环境         |
| `IsOffline()`                        | `local`、`dev` 或 `test`     |
| `IsOnline()`                         | `pre` 或 `prod`              |

## 调试开关

`AppDebug` 优先读取 `APP_DEBUG`，其次读取 `KRATOS_DEBUG`。变量不存在时返回 `false`；存在时按 `strconv.ParseBool` 解析，非法值会
panic。

## 通用读取

```go
address := env.GetEnv("SERVICE_ADDRESS", "127.0.0.1:8080")
enabled := env.GetEnvAsBool("FEATURE_ENABLED", false)
```

- 环境变量只要存在就优先于默认值，包括值为空字符串的情况。
- 可选默认值只使用第一个；通常只应传零个或一个默认值。
- `GetEnvAsBool` 对存在但非法的值执行 panic。

## 使用约束

- 只在启动和依赖组装阶段读取这些值，不要用环境变量承担请求级动态配置。
- 不要把密码、token 或密钥写入日志；本包不会自动过滤敏感值。
- 需要把配置错误返回给调用方而不是 panic 的公共库，应自行使用 `os.LookupEnv` 并返回错误。
