# Job

`pkg/job` 是业务与 Wire 声明、构造和注册后台任务的公共入口。业务使用 `Spec` 注册 Cron、Once 或 Daemon 任务，再通过 `NewManager` 构造运行时并交给 `bootstrap.NewJobBootstrap`；不应导入 `pkg/job/internal/*`。

```go
spec := job.NewSpec()
spec.Cron("cleanup", "@every 1m", cleanupTask, job.WithConcurrentPolicy(job.SkipIfRunning))
manager, err := job.NewManager(logger, spec, tracingProvider, metricsProvider)
contribution, err := bootstrap.NewJobBootstrap(appSpec, manager)
```

跨进程并发策略由业务显式注入 `ConcurrencyCoordinator`。Redis 实现位于公共 `contrib/job/redis`：

```go
coordinator, err := jobredis.NewLockCoordinator(
	redisManager,
	job.LockCoordinatorConfig{},
	lockredis.WithConnection("locks"),
)
spec.Coordinator(coordinator)
```

Job Runtime 在 `Start` 时立即启动。`ExitWhenDone` 在所有 Once 任务成功完成后返回 `job.ErrCompleted`；组装层 `bootstrap.NewJobBootstrap` 的适配器将该结果转换为 `app.ErrStopRequested`，请求正常停机。任务自身的失败原样保留。直接使用 Manager 时，由调用方处理 `job.ErrCompleted`。

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

`bootstrap.NewJobBootstrap` 仅在 Manager 包含任务时，同步追加 Runtime；空 Manager 不作登记。它不创建资源也不返回 cleanup，Manager 的 Start/Stop 由 `app.NewApp` 创建的应用生命周期监督层拥有。

Job 不使用全局驱动注册表，也不会读取 `job.lock.driver` 自动选择实现。任务与协调器公共契约、调度、并发策略和观测逻辑直接定义在 `pkg/job`。`manager.go` 负责构造与任务组装，`manager_lifecycle.go` 负责运行和收敛；`cron.go` 集中调度与表达式解析，`log.go` 集中任务及 cron 日志适配；`lock_coordinator.go` 负责锁获取与配置校验，`lock_guard.go` 负责租约续租释放。

租约续租失败或超过 `OperationTimeout` 时，执行 Context 会独立取消并携带 `ErrCoordinationLost`，即使底层 `Refresh` 尚未返回；晚到的成功不会恢复旧任务。Handler 应响应 Context 取消。协调器不会重新拿锁继续旧任务，后续周期调度仍按原计划运行。

Release 先取消旧执行和续租，再最多等待 `OperationTimeout` 让在途续租退出。超过等待预算便返回错误，不与在途刷新并发 Unlock，而让租约自然过期。Redis 在途 I/O 仍遵守原客户端配置；默认读取超时有限。显式配置无限读取且禁用 Context 超时时，旧任务仍及时取消、Release 仍可返回，但原续租调用需等网络返回或 Redis Manager cleanup 关闭连接后收尾。第三方 Lease 仍须遵守 Context 约定，完全不可取消的实现无法被 Go 强制终止。协调器不为每次调用额外创建阻塞 goroutine；超时只触发短取消回调。
