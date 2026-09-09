# Redis Job Coordinator

`contrib/job/redis` 把公共 Redis Manager、Redis Locker 和通用 Job 并发协调器组合成业务/Wire 可直接选择的实现。

```go
coordinator, err := jobredis.NewLockCoordinator(
	redisManager,
	job.LockCoordinatorConfig{},
	lockredis.WithConnection("locks"),
)
if err != nil {
	return err
}
jobSpec.Coordinator(coordinator)
```

构造阶段只选择并借用 `pkg/redis.Manager` 持有的连接，不执行 Redis 命令，也不接管连接关闭。实际获取、续租和释放锁发生在任务执行期间。

Job 锁实现由业务/Wire 显式选择；这里没有全局驱动注册表，也不会根据 `job.lock.driver` 自动分发。需要其他协调方案时，业务可以直接构造实现 `job.ConcurrencyCoordinator` 的公共组件并交给 Job Spec。
