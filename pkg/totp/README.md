# totp

`totp` 实现 RFC 6238 时间型一次性密码，默认与 Google Authenticator 等 `otpauth` 应用兼容。

## 创建密钥与验证器

```go
secret, err := totp.GenerateSecret()
if err != nil {
	return err
}

authenticator, err := totp.New(secret)
if err != nil {
	return err
}
```

`GenerateSecret` 使用密码学安全随机源生成 20 字节密钥，并编码为无填充的大写 Base32。`New` 接受大小写不敏感、可带尾部 `=` 填充的 Base32；解码后的密钥不得少于 10 字节。新生成或导入的密钥建议至少包含 16 字节随机数据。

## 生成与校验

```go
code := authenticator.Current()

if !authenticator.Authenticate(userInput) {
	return errInvalidCode
}
```

| API | 用途 |
| --- | --- |
| `Current()` | 生成当前时刻的验证码 |
| `CodeAt(time.Time)` | 生成指定协议时间步的验证码 |
| `Authenticate(string)` | 按当前时刻及漂移窗口校验验证码 |
| `AuthenticateAt(string, time.Time)` | 按指定时刻校验验证码 |
| `Secret()` | 返回标准化后的 Base32 密钥 |

验证码必须是配置位数的纯数字。比较使用恒定时间操作。

## 选项

`Option` 是传给 `New` 的配置函数类型。应使用包内提供的 `WithPeriod`、`WithDigits` 和 `WithSkew` 构造选项，不要自行修改 `Authenticator` 的内部状态。

```go
authenticator, err := totp.New(
	secret,
	totp.WithPeriod(30*time.Second),
	totp.WithDigits(6),
	totp.WithSkew(1),
)
```

- 默认周期为 30 秒；自定义周期必须是正整数秒。
- 验证码支持 6 位或 8 位，默认 6 位。
- `skew` 表示当前时间步前后额外接受的步数，默认 1、最大 2；值越大，验证码可被利用的时间窗口越长。
- `nil` option 会使 `New` 返回错误。

## Provision URI

```go
uri, err := authenticator.ProvisionURIWithIssuer(
	"alice@example.com",
	"Example",
)
```

`ProvisionURI` 和 `ProvisionURIWithIssuer` 返回 `otpauth://totp/...` URI，可交给二维码组件显示。account 必须非空；issuer 会同时写入 label 和 query。

## 安全边界

- 每个用户必须使用独立密钥，并对数据库中的密钥进行访问控制或加密保护。
- 本包只判断代码在时间窗口内是否正确，不保存使用记录。认证服务必须在持久化存储中记录成功消费的时间步，防止同一验证码在有效窗口内重放。
- 验证接口外层必须实施尝试次数限制、审计和账户级速率限制。
- Provision URI 包含完整密钥，不得写入日志、分析事件或错误消息。
- 服务器时钟应保持同步；不要用增大 `skew` 掩盖长期时钟漂移。
