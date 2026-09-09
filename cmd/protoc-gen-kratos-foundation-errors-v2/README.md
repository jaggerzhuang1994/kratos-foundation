# protoc-gen-kratos-foundation-errors-v2

使用仓库根目录的 `make proto` 会构建当前插件并重新生成内部错误函数。
业务协议需要更新插件后重新运行对应的 protoc 命令。

插件名称已添加 `-v2` 后缀，业务 protoc 命令应使用
`--kratos-foundation-errors-v2_out`；显式指定插件时使用
`--plugin=protoc-gen-kratos-foundation-errors-v2=/path/to/protoc-gen-kratos-foundation-errors-v2`。

`ErrorXxx` 保留 main 的字符串首参数格式化调用：

```go
ErrorValidator("user %s not found", name)
```

无参数使用 Proto 注释，单参数原样转为字符串（例如 `100% complete` 不会被当成模板），
多个参数且首参数为 string 时调用 `fmt.Sprintf`，非 string 首参数使用 `fmt.Sprint`。
从此前 v2 的片段拼接行为迁移时，显式使用 `fmt.Sprint(parts...)` 再传一个字符串。
新代码建议显式构造消息以消除歧义：

```go
ErrorValidator(fmt.Sprintf("user %s not found", name))
ErrorValidator(fmt.Sprint("user ", name, " not found"))
```

Proto 注释包含格式指令时生成的 `ErrorXxxWithFormat` 仍使用注释作为格式模板。
`IsXxx` 以 reason 和 reason_code 作为业务身份，不以 HTTP 状态作为身份的一部分，
因此改变传输状态或经过 gRPC 转换不影响业务错误识别。

```mermaid
flowchart TD
    A([调用 ErrorXxx]) --> B{参数数量}
    B -- 零 --> C[使用 Proto 注释]
    B -- 一个 --> D[Sprint 保留原文]
    B -- 多个 --> E{首参数是 string?}
    E -- 是 --> F[Sprintf 格式化]
    E -- 否 --> D
    C --> G[构造错误值 附 reason_code 和内部堆栈]
    D --> G
    F --> G
    G --> H([返回错误 由调用边界决定日志与响应])
```
