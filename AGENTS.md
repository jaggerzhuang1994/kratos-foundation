# AGENTS.md

本仓库是公共 Go 基础库，被多个业务项目直接依赖。任何改动都可能影响所有消费方，因此以**不改变既有行为**为第一原则。

标记含义：**【必须】** 违反即视为缺陷；**【禁止】** 不得出现；**【指导】** 推荐做法，偏离需有理由；**【建议】** 视情况采用。

优先级：用户当前任务 > 更深目录的 `AGENTS.md` > 本文件 > 现有代码惯例。

## 1. 工作方式

- **【必须】** 开始前检查 `git status`，保留用户已有改动；只修改任务涉及的文件。
- **【必须】** 改变公共行为、默认值、错误语义、日志事件名或配置字段前，先向用户说明影响并取得确认；确认后在 `CHANGELOG.md` 的“计划中”记录，破坏性变更写入“升级风险”。
- **【必须】** 发现共享可变状态、竞态、重复执行或缓存一致性风险时，先告知用户风险与可选方案（互斥锁、读写锁、原子操作、事务、幂等、串行化等），**确认后**再引入或改变并发控制，并说明保护对象、粒度及对性能和死锁的影响。
- **【必须】** 交付时说明实际执行的命令及结果、未执行的验证和剩余风险；不得把静态核对说成运行验证。
- **【禁止】** 未经授权执行推送、打 tag、发布或修改外部环境的操作。
- **【禁止】** 顺带进行与任务无关的重构、重命名或风格统一。
- **【建议】** 简单局部任务用简短计划即可；涉及多个包或行为变化时，先列出受影响的调用方与文档再动手。

## 2. 目录与包边界

```text
pkg/<domain>/                      公共契约、构造入口与同领域实现
pkg/<domain>/internal/<capability>/ 职责独立的私有子能力
contrib/<domain>/<driver>/         可选第三方实现（MySQL、Consul、阿里云 OSS 等）
internal/<capability>/             至少两个领域复用的私有能力与测试设施
proto/、third_party/               内部协议及共享 include 协议
cmd/protoc-gen-*/                  独立 Go 模块的代码生成器
examples/                          用法示例代码（可编译，不负责运行环境）
observability/                     Grafana 面板、Prometheus 规则与接入说明
test/                              可在本地运行的测试环境与脚本
```

- **【必须】** 领域包只提供普通构造函数和自身依赖声明；组装统一在 `pkg/bootstrap` 与业务 Wire 层完成。
- **【禁止】** 领域包导入 `app`、`bootstrap` 或 Wire；一个领域导入另一个领域的 `internal`；嵌套 `internal` 反向依赖所属领域包。
- **【禁止】** 把整个领域搬进 `internal/` 再用类型别名转发成公共空壳；仅为测试或控制行数新建包。
- **【必须】** 第三方 SDK 的类型、配置转换与错误转换限制在 `contrib` 适配层，核心包不依赖具体 `contrib` 实现。
- **【必须】** 只有同时满足“一个 Manager 管理多份具名资源、每项按 driver 选择实现、Manager 负责生命周期”时才使用 Driver Registry（当前为 database、oss、registry）。驱动 `init` 只注册无状态 factory，禁止读配置、I/O、创建资源或启动 goroutine。
- **【必须】** 对外提供公共 API 的包维护 `README.md`，从调用方视角说明用法、约束与资源所有权。
- **【指导】** 文件按职责命名（`producer.go`、`transaction.go`），手写文件通常 150–300 行，超过 400 行检查是否混合了多个职责。

## 3. Go 代码

- **【必须】** 语法不超过所属模块 `go.mod` 的 `go` 版本；`cmd/protoc-gen-*` 是独立模块，版本可能低于根模块。
- **【必须】** 处理所有有意义的错误；补充上下文用 `%w`，判断用 `errors.Is` / `errors.As`；错误信息小写开头、无句号。
- **【必须】** `context.Context` 作为第一个参数传入，不保存到结构体，不传 nil。
- **【必须】** 启动 goroutine 时明确其退出条件与所有权；持锁期间不做网络、磁盘或调用不受控代码。
- **【必须】** 返回 cleanup 的资源由 Wire 或组装层逆序释放；操作级租约（如客户端 `release`）由调用方释放。
- **【必须】** 导出标识符具有 GoDoc 注释；为非直观的分支、默认行为、生命周期和兼容约束写简洁中文注释。
- **【禁止】** 库代码调用 `os.Exit`、`log.Fatal`；无理由的全局可变状态；无法停止的后台 goroutine。
- **【禁止】** 为测试新增仅供测试使用的公共 API、字段、选项或分支。
- **【禁止】** 对构造函数注入的必需依赖重复判空（接口明确允许 nil 的可选依赖除外）；过度防御。
- **【禁止】** 返回给外部调用方的错误泄漏内部路径、凭据、SQL；错误堆栈 `err_stack` 只能经 `env.ExposeDiagnostics()` 判断后返回，prod 一律不返回。
- **【指导】** 接受接口、返回具体类型；接口定义在使用方、按实际调用最小化；可选行为优先用配置结构体，谨慎使用 functional options。
- **【指导】** 仅在有测量依据时做性能优化，并附 benchmark。

## 4. 日志与可观测性

框架日志由 `TestFrameworkLogConventions` 扫描源码校验：

- **【必须】** `event` 为 `<领域>.<对象>.<动作>`，全小写、段内下划线，领域与 `module` 一致；动作用过去分词或状态词（`started`、`applied`、`rejected`、`failed`、`retrying`、`recovered`、`restart_required`）。
- **【必须】** `msg` 为小写英文短语、主语开头、不超过 8 个词、无句号；固定句式：`<对象> rejected; keeping previous`、`<对象> change requires restart`、`<对象> failed; retrying`。
- **【必须】** 字段只带排障必需信息（通常 ≤5 个）：错误用 `error`，分类原因 `reason`，耗时 `duration`（`time.Duration`），预算 `timeout`，次数 `attempt`/`attempts`；`ts`、`caller`、`module`、服务身份与 trace 由 Logger 自动附加，不重复写入。
- **【必须】** 级别：生命周期与成功的配置变更 INFO；可自动恢复的异常与重试 WARN；需要人工介入的失败 ERROR；逐条高频明细 DEBUG。同一错误只在能决定处理方式的边界记录一次。
- **【禁止】** 记录密码、令牌、密钥、证件号、完整手机号等敏感信息；把用户 ID、请求 ID 等高基数值作为指标标签。
- **【必须】** 修改指标名称或标签时同步更新 `observability/` 下的面板、规则及 `test/observability` 的断言。

## 5. 测试

- **【必须】** 每个含手写逻辑的包在本目录提供独立测试；`make test` 会拒绝没有 `TestXxx` 的包和 0% 覆盖的手写函数。
- **【必须】** 一个实现文件最多对应一个同名测试文件（`foo.go` ↔ `foo_test.go`）；单元、集成、竞态、benchmark、回归测试归入同一文件，不按场景后缀拆分；helper 与 fixture 放在其主要服务的测试文件中。
- **【必须】** 修复缺陷时添加能复现该缺陷的回归测试；修改并发代码运行 `make race` 或覆盖受影响模块的 `-race` 测试。
- **【必须】** 测试确定性：不依赖执行顺序、真实时间、外部网络或共享环境；使用 `t.TempDir()`、`t.Setenv()`、`t.Helper()`。
- **【必须】** 依赖真实外部服务的测试只能通过 `test/external` 的隔离环境运行，不得默认依赖开发者机器上已有的服务。
- **【指导】** 多组输入用表驱动与子测试；优先断言公共行为与最终状态，而非内部调用次数；Mock 只针对小接口。
- **【指导】** 新增用例前检查已有覆盖，能扩展就不另起；重构时同步清理失去意义的重复测试，但保留并发、资源释放、失败回退等独立回归保护。

## 6. 生成代码与依赖

- **【禁止】** 手工修改 `*.pb.go`、`*.pb.validate.go`、`wire_gen.go`、`config.schema.json` 和 `go.sum`。
- **【必须】** 修改 proto 后运行 `make proto`；修改 Wire injector 或影响其构造签名后运行 `make generate`，并确认产物实际更新。
- **【必须】** 新增依赖前确认必要性、维护状态、许可证与漏洞；修改依赖后在受影响模块执行 `go mod tidy` 并运行 `make vulncheck`。
- **【必须】** 依赖升级若改变运行时行为（默认值、网络调用、错误语义），暂缓升级并向用户说明，不能只看测试是否通过。
- **【禁止】** `replace` 长期指向本地路径；为少量功能引入大型依赖。

## 7. 文档

- **【必须】** 修改公共符号、构造签名、配置、错误语义、生命周期或资源所有权时，用 `rg` 搜索旧名称，同步根 README、包 README、`config.example.yaml`、示例与 CHANGELOG，完成后复查旧引用。
- **【必须】** 文档描述以实际代码为准：写清生效前提、作用范围、默认值与显式零值的区别、是否热更新、失败边界和资源所有权；不得把示例值写成默认值，不得把条件性保证写成无条件保证。
- **【必须】** 文档中的代码示例与当前签名一致；修改关键示例时编译验证（可在临时包中 `go vet`）。
- **【指导】** 详细用法集中在所属包 README，根 README 只放能力概览与通用用法，用链接代替复制。
- **【建议】** 描述复杂流程（生命周期、重试、并发、停机）时附 Mermaid 流程图，标出异常与超时分支及对应日志事件；图与实现须保持一致。
- **【禁止】** 提交 AI 过程文档（规划、方案、审查记录）到仓库；结论应沉淀到包 README、CHANGELOG 或提交说明。

## 8. 命令

先 `make help` 查看可用命令，使用前阅读 target 定义；工具由 `make init` 安装到 `tools/` 并锁定版本，Wire 通过 `go.mod` 的 `tool` 指令以 `go tool wire` 调用。

| 改动类型 | 至少运行 |
| --- | --- |
| Go 逻辑 | `make lint`，相关包测试；并发代码加 `-race` |
| 公共行为 / 多个包 | `make verify`（test + vet + race）与 `make lint` |
| Wire 组装、Bootstrap | `make generate`、`make test-business` |
| 依赖 | `go mod tidy`、`make vulncheck`、`make verify` |
| proto / 配置协议 | `make proto` 并检查 `config.schema.json` 差异 |
| 面板、规则、指标 | `make test-observability`（需要 Docker） |
| 纯文档 | 核对事实、示例与链接，`git diff --check` |
| 发布前 | `make verify-release`；外部服务相关改动加 `make test-external` |

- **【必须】** 优先使用 Makefile 已有 target；修改 Makefile 时为新 target 添加 `.PHONY` 与 `##` 帮助说明，并保证失败返回非零。
