# errors

`errors.go` 定义错误值、通用操作与堆栈处理，`metadata.go` 管理错误附加信息，`grpc.go` 负责 gRPC 状态转换，`validation_error.go` 处理校验错误。
`errors` 在 Kratos 状态错误之上统一 HTTP 状态、gRPC 状态、业务原因码、公开元数据、参数校验信息和内部错误链。业务代码创建错误，传输边界负责选择如何对外编码。

## 创建与包装

```go
err := errors.New(
http.StatusUnprocessableEntity,
"VALIDATION_FAILED",
"name is required",
).
WithReasonCode(42201).
WithMetadata(map[string]string{"request_id": requestID}).
WithCause(cause)
```

`New(code, reason, message)` 中：

- `code` 是 HTTP 状态码；
- `reason` 是稳定、适合程序判断的原因标识；
- `message` 是允许对外展示的消息。

所有 `With...` 方法都返回副本，不修改原错误，因此可以安全地从一个错误模板派生多个实例。

| API                   | 用途                                                |
|-----------------------|-----------------------------------------------------|
| `WithCause`           | 保存内部原因并支持标准 `errors.Is`/`errors.As` 遍历 |
| `WithErrStack`        | 保存调用栈；仅在 `%+v` 或 `ErrStack` 中展开         |
| `WithReasonCode`      | 设置独立于 HTTP 状态的业务数值码                    |
| `WithMetadata`        | 合并可公开的字符串元数据                            |
| `WithHTTPData`        | 设置 HTTP 响应中的结构化 `data`                     |
| `WithHTTPHeaders`     | 合并 HTTP 错误响应头并去重                          |
| `WithValidationError` | 附加 protobuf 参数校验失败列表                      |

`PublicMetadata`、`HTTPData`、`HTTPHeaders`、`ValidationError`、`ReasonCode` 和 `ErrStack` 分别读取对应状态。`GRPCStatus`
用于服务端 gRPC 编码；`Unwrap`、`Is`、`Error` 和 `Format` 由 Go 错误协议及格式化接口调用，业务代码通常无需直接调用。

## 读取错误

```go
statusCode := errors.Code(err)
reason := errors.Reason(err)
message := errors.Message(err)
reasonCode := errors.ReasonCode(err)
```

这些辅助函数支持包装错误。对 `nil` 错误，`Code` 和 `ReasonCode` 返回 200，`Message` 返回空字符串，HTTP data 为空，HTTP header
为空集合。

`FromError` 的处理顺序是：

1. 从错误链中查找本包的 `*Error`；
2. 尝试恢复 gRPC status 及 `ErrorInfo`；
3. 其他错误转换为 500/UNKNOWN，并把原错误文本作为消息。

本包的状态错误使用 HTTP 状态码和 reason 参与 `errors.Is` 匹配。底层 cause 仍可通过 `errors.Is` 和 `errors.As` 访问。

## HTTP 与 gRPC 边界

`GRPCStatus` 把 HTTP 状态映射为 gRPC code，并通过 `google.rpc.ErrorInfo` 传递 reason、业务原因码和公开元数据。对于无法由标准
gRPC code 唯一表示的 HTTP 错误状态，会携带受校验的原始 HTTP 状态码以便往返恢复。

以下内部字段不会出现在 `PublicMetadata` 中：错误栈、业务原因码、HTTP 状态恢复字段、HTTP data、HTTP header
和参数校验详情。不要把秘密、凭据或只供日志使用的内部信息放入普通 metadata。

`WithHTTPData` 面向可 JSON 编码的数据。读取 `HTTPData` 得到独立副本；调用方不应依赖自定义 Go 类型在 JSON 边界后仍保持原具体类型。
数字以 `json.Number` 保留，避免大整数或高精度小数在快照复制和 HTTP 往返中被 `float64` 舍入；需要计算时由调用方显式转换。

## 参数校验错误

`ParseValidationError` 识别 protoc-gen-validate 生成的单条或聚合校验错误，并转换为稳定的 `ValidationError`：

```go
validationErrors := errors.ParseValidationError(request.ValidateAll())
err := errors.New(400, "VALIDATION_FAILED", "invalid request").
WithValidationError(validationErrors)
```

无法识别的非空错误会保留为一条 `unknown` 记录；传入 `nil` 返回 `nil`。

`ValidationError` 实现 `error`、`json.Marshaler` 和 `json.Unmarshaler`，对应方法为 `Error`、`MarshalJSON` 和
`UnmarshalJSON`。它的公开字段 `Field`、`Reason`、`Cause`、`Key` 和 `ErrorName` 是稳定传输表示；调用方应优先通过
`ParseValidationError` 构造，而不是依赖具体生成器错误类型。

`SupportPackageIsVersion1` 只供生成代码做编译期兼容性断言，业务代码不应引用。

## 日志与公开信息

- `Error()`、`%s` 和 `%v` 只输出公开状态与公开 metadata。
- `%+v` 和 `ErrStack` 会包含调用栈及底层 cause，只能写入受控的内部日志。
- `message`、HTTP data、validation error 和普通 metadata 都可能离开进程，必须在创建错误时完成脱敏。
- 底层包应返回带 `%w` 的原因错误；决定 HTTP/gRPC 呈现的边界层再创建本包状态错误。
