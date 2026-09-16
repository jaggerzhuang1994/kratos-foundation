# Job

`pkg/job` 声明 Cron、Once 和 Daemon 任务。`bootstrap.Spec.Job()` 使用 Wire 共享的 job.Spec；`bootstrap.NewJobBootstrap` 注入应用的 config.Manager，构造 Manager 并登记 Runtime。Job 只提供本进程内、同一 Manager 中同一注册名称的并发控制；不同 Manager 和进程互不协调。

## 声明与优先级

Cron 四项参数逐字段按 **配置 > 注册时显式指定 > Task 自身声明 > 框架默认值** 解析。Task 可分别实现以下可选接口，不必全部实现；方法只在 Manager 构造时调用一次，后续热更新复用这份基线。

| 参数 | 配置字段 | 注册声明 | Task 可选方法 | 框架默认值 |
| --- | --- | --- | --- | --- |
| 定时规则 | `schedule` | RegisterCron 的非空 schedule | `Schedule() string` | 无，合并后必须非空且合法 |
| 并发策略 | `concurrent_policy` | `WithConcurrentPolicy(...)` | `ConcurrentPolicy() job.ConcurrentPolicy` | `AllowOverlap` |
| 启动立即执行 | `run_immediately` | `RunImmediately(bool)` | `RunImmediately() bool` | false |
| Delay 容量 | `max_pending_runs` | `WithMaxPendingRuns(int)` | `MaxPendingRuns() int` | 1 |

下面是可放入业务组装包的完整声明示例。Task 的 Run 承担业务工作，Boot 只登记已注入的 Task。

```go
package assembly

import (
    "context"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
)

type RefreshTask struct{}
func (*RefreshTask) Run(ctx context.Context) error { return ctx.Err() } // 替换为实际业务逻辑。
func (*RefreshTask) Schedule() string { return "@every 1m" }
func (*RefreshTask) ConcurrentPolicy() job.ConcurrentPolicy { return job.SkipIfRunning }
func (*RefreshTask) RunImmediately() bool { return true }
func (*RefreshTask) MaxPendingRuns() int { return 2 }

func Boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, task *RefreshTask) bootstrap.Bootstrap {
    spec.Job().RegisterCron("refresh", "", task, job.RunImmediately(false))
    return bootstrap.Bootstrap{}
}
```

空注册表达式表示使用 Task 或配置。配置中的空字符串是显式覆盖，因表达式无效而报错。配置使用 optional 字段，false、0、ALLOW_OVERLAP 都是有效的显式覆盖。有效快照中删除某个覆盖字段或将其设为 null，会重新使用注册/Task 基线。来源如何合并和删除字段仍遵循 [Config 语义](../config/README.md#来源与合并)。修改已经传入的 Spec 或 Task 默认方法不产生热更新。

```yaml
job:
  cron:
    refresh:
      schedule: "*/10 * * * * *"
      concurrent_policy: DELAY_IF_RUNNING
      run_immediately: false
      max_pending_runs: 1
```

表达式支持五字段、六字段（含秒）以及 `@hourly`、`@every 1m` 等描述符，时区由 `job.WithLocation` 指定。配置只覆盖已注册 Cron；未知任务名和 Once/Daemon 名称均拒绝，不能通过配置动态创建 Task。三种并发枚举为 `ALLOW_OVERLAP`、`DELAY_IF_RUNNING`、`SKIP_IF_RUNNING`；旧分布式策略不再支持。

## 配置热更新与生命周期

`NewManager(logger, spec, tracing, metrics, configManager)` 构造时读取并验证配置；configManager 可显式传 nil，表示只用静态声明。Bootstrap 正常注入应用 config.Manager。Start 再读取最新快照并建立 `job` 订阅，Stop 取消订阅；不额外返回 cleanup。由 App 管理 Start/Stop，应用先停止任务，再清理 Config Manager、日志和遥测资源。

每次更新先完整校验全部任务，任一表达式、策略、容量或名称无效则整批拒绝，记录 `ERROR job.config.rejected` 并保留上一份有效规则；初次构造或启动时无效则返回错误。合法变更记录 `INFO job.config.applied`。Config Manager 按轮询快照通知，短时间多次更新可能合并。

新表达式重新计算后续调度，旧周期不补跑。调度控制循环通过替换 robfig 条目应用更新，不等待正在执行的 Task。运行中修改 run_immediately 不额外触发：它只影响下一次进程启动；构造后、Start 前的配置变化仍会影响本次启动。

并发规则与容量作用于后续触发；正在执行的任务继续，已经排队的调用仍等待串行执行。切换策略保留运行计数，AllowOverlap 改成 Skip/Delay 时不会丢失旧调用。缩容不丢弃已有等待，只限制新增等待；切到 AllowOverlap 后，新调用可能先于旧等待执行，不保证严格 FIFO 或公平性。容量只限制 Delay 的等待数，不限制 AllowOverlap 的并行数。

```mermaid
flowchart TD
 A([构造 Manager]) --> B[读取 Task 和注册基线 合并 job.cron]
 B --> C{整批校验通过?}
 C -- 否 --> X([返回错误])
 C -- 是 --> D[Start 重读配置 建立订阅]
 D -- 失败 --> X
 D -- 成功 --> E[启动调度控制循环和任务]
 U([Config 异步回调]) --> V[锁外解析整批快照]
 V -- 无效 --> W[ERROR job.config.rejected 保留旧规则]
 V -- 有效 --> L[获取 Manager 状态锁]
 L --> S{正在停机?}
 S -- 是 --> Z[释放锁 忽略更新]
 S -- 否 --> G[逐任务获取 gate 锁 更新策略并释放 gate 锁]
 G --> H[发布新调度声明 释放 Manager 锁]
 H --> I[INFO job.config.applied]
 I --> J[控制循环锁外替换 robfig 条目]
 J --> E
 W --> E
 E --> K([Stop 或父 Context 取消])
 K --> M[状态锁内标记停止 取出取消函数 释放锁]
 M --> N[锁外取消订阅和任务 通知调度控制循环退出]
 N --> O[等待执行和等待中的任务退出]
 O -- 超过 Stop Context --> P([返回超时 收敛继续])
 O -- 完成 --> Q([结束])
 Z --> Q
```

## Delay 容量

`DelayIfRunning` 默认最多一轮执行、一轮等待。`max_pending_runs=0` 不保留等待，-1 表示无界等待；小于 -1 或最大 int 拒绝。满额触发跳过并记录 WARN，不调用 ErrorHandler。需要逐轮可靠处理的工作使用持久队列。

`WithDelayOverflowHandler(func(context.Context, job.DelayOverflow) error)` 可注入满额通知，事件含 Name、Policy、MaxPendingRuns。回调在锁外同步调用，不占执行名额；可能并发发生，业务负责并发安全、超时和去重。回调成功仍跳过本轮，错误或 panic 转换结果交给 Cron ErrorHandler；框架不重试。配置热更新不替换此回调。

```mermaid
flowchart TD
 A([Cron 并发触发]) --> B[获取本任务 gate 互斥锁]
 B --> C{Context 已取消?}
 C -- 是 --> R[释放锁 返回取消]
 C -- 否 --> D{允许重叠 或没有运行和等待?}
 D -- 是 --> E[增加运行数 释放锁]
 D -- 否 --> F{Skip 策略?}
 F -- 是 --> S[释放锁 WARN job skipped]
 F -- 否 --> G{Delay 等待已满?}
 G -- 是 --> H[释放锁 WARN pending-run queue is full]
 H --> I[锁外调用可选业务通知 含外部超时]
 I -- 错误或 panic --> J[交给 ErrorHandler]
 I -- 成功 --> Z([跳过结束])
 G -- 否 --> K[增加等待数]
 K --> K1[释放锁 等待运行完成或取消]
 K1 --> L[重新获取 gate 锁检查状态]
 L -- 取消 --> M[减少等待数 释放锁 返回取消]
 L -- 仍有运行 --> K1
 L -- 可执行 --> N[减少等待数 增加运行数 释放锁]
 E & N --> O[锁外执行 Task 和业务中间件]
 O --> P[defer 获取 gate 锁 减少运行数 唤醒等待 释放锁]
 P --> T([返回结果])
 R & M & J & S --> Z
```

Job Runtime 在 `Start` 时立即启动。`ExitWhenDone` 要求至少注册一个 Once，且 Spec 只能包含 Once；混入 Cron 或 Daemon 会在 `NewManager` 校验时返回错误。该模式在所有 Once 任务成功完成后返回 `job.ErrCompleted`；组装层 `bootstrap.NewJobBootstrap` 的适配器将该结果转换为 `app.ErrStopRequested`，请求正常停机。任务自身的失败原样保留。直接使用 Manager 时，由调用方处理 `job.ErrCompleted`。

```mermaid
flowchart TD
    A([声明 ExitWhenDone]) --> B{至少一个任务且全部为 Once?}
    B -- 否 --> C([NewManager 返回校验错误])
    B -- 是 --> D[应用 Start 并发执行 Once]
    D -- 全部完成 --> E{存在任务失败?}
    E -- 是 --> F([Start 返回聚合错误，应用按失败处理])
    E -- 否 --> G[Start 返回 ErrCompleted]
    G --> H[Bootstrap 转为 ErrStopRequested]
    H --> I([应用正常停机])
    D -- Stop 或父 Context 取消 --> J[取消任务并开始收敛]
    J --> K([退出等待；Stop 的等待受其 Context 限制])
```

停止期间只忽略正常返回和纯 Context 取消错误。若任务把取消与业务失败通过 `errors.Join` 合并，业务失败仍交给原有错误处理边界，不会当作正常停止而丢弃。

```mermaid
flowchart TD
    A([任务返回]) --> B{管理器 Context 已取消?}
    B -- 否 --> C[保留任务结果]
    B -- 是 --> D{结果为空或错误树全部叶子匹配取消原因?}
    D -- 是 --> E[视为正常停止]
    D -- 否 --> C
    C --> F[由原有返回值或 ErrorHandler 处理]
    E --> G([结束])
    F --> G
```

`bootstrap.NewJobBootstrap(application, jobs, configManager, logger, metrics, tracing)` 只构造和登记有任务的 Runtime，空 Spec 不登记。Job 不依赖 app/bootstrap，不管理全局容器，也没有 Coordinator、锁租约或 Redis 适配。`manager.go` 管构造，`manager_lifecycle.go` 管启停，`config.go` 管优先级，`config_reload.go` 管订阅与更新，`concurrent_policy.go` 管持续存在的本进程执行状态。

任务和中间件通过 `job.JobNameFromContext(ctx)` 读取注册名；非任务 Context 返回空字符串。

共享 Grafana 组件面板、指标名称与采集边界见 [组件指标说明](../../deploy/observability/docs/components.md)。

## 集成测试与边界用法

参见[核心组件集成用例](../INTEGRATION_TESTS.md#扩展模块与常见边界)。根目录 `make test-components` 运行自包含组合；`make test-components-external` 创建隔离 Docker 服务，验证真实 Kafka、Redis 和锁等功能。具体场景、所有权及适用边界见用例说明。

任务中间件记录开始、成功和正常取消；错误与 panic 仅交给最终 ErrorHandler。默认处理器携带 Context 记录一次最终错误；自定义 `WithErrorHandler` 时由业务负责最终错误日志。

```mermaid
flowchart TD
 A([执行任务]) --> B[INFO job execution started]
 B --> C{执行结果}
 C -- 成功 --> D[INFO job execution done]
 C -- 正常取消 --> E[INFO job execution stopped]
 C -- 错误或恢复后的 panic --> F[调用最终 ErrorHandler]
 F --> G[默认 ERROR job failed 或 cron job failed；自定义由业务处理]
 D --> H([结束])
 E --> H
 G --> H
```

## 中间件执行顺序

Cron 的进入顺序为：recovery → 并发控制 → tracing → metrics → logging → 按注册顺序的业务中间件 → Task；返回及 defer 按相反顺序执行。Once、Daemon 使用同一顺序，但没有并发控制。关闭观测只移除对应观测层，recovery 始终位于整个执行链最外层，且只组装一次。

观测层进入时将结果初始化为 panic 失败标记，只有下层正常返回才用返回值覆盖。因此 panic 展开期间不会按 nil error 记录成功，运行指标会归还、span 会以失败状态结束。最外层 recovery 统一将 panic 转为携带原始内容与堆栈的错误，交给既有结果处理边界。业务 panic 的 trace 使用通用 `job execution panicked` 描述，详细错误由最终 ErrorHandler 接收。

观测实现自身 panic 时只能保证返回错误，不能保证该实现已中断的指标或 span 完整。构造中间件时、最终 ErrorHandler 内及 Task 自建 goroutine 中的 panic 不在这条执行链的保护范围内。

```mermaid
flowchart LR
 A([任务触发]) --> R[最外层 recovery]
 R --> G[并发控制 仅 Cron]
 G -- 跳过或取消 --> R
 G -- 获得执行名额 --> T[tracing 初始化失败标记]
 T --> M[metrics 初始化失败标记]
 M --> L[logging 初始化失败标记]
 L --> B[业务中间件 按注册顺序]
 B --> J[Task]
 J -- 正常返回覆盖标记 或 panic 保留标记 --> L
 L -- defer 仅正常成功记录 done --> M
 M -- defer 记录结果和耗时 归还运行指标 --> T
 T -- defer 设置 span 状态并结束 --> G
 G -- defer 归还执行名额 --> R
 R -- 正常结果或 panic 转为含堆栈错误 --> E[统一结果处理 错误交给最终 ErrorHandler]
 E --> Z([结束])
```
