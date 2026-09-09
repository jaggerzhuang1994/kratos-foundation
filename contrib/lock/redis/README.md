# Redis 分布式锁

Locker 借用 `pkg/redis.Manager` 的连接，不持有独立连接池，也不负责关闭客户端。

`Lock` 在尚未取得租约时等待锁竞争，并对暂时网络错误使用从 100ms 开始、翻倍到 5s、带抖动的可取消退避；收到锁竞争结果后重置网络退避。`TryLock` 仍只尝试一次。任务取得租约后不能通过重新拿锁延续旧执行，续租及释放只操作原租约令牌。

锁脚本继续使用 Manager 的原客户端，保留已有命令重试、追踪和指标 hooks。退避等待可被 Context 取消；已发出的网络读写仍受 Redis 的 `read_timeout`、`write_timeout`、`context_timeout_enabled` 配置约束，不能保证只取消 Context 就立即中断在途 I/O。默认读取超时有限；显式配置无限读取且不启用 Context 超时时，需要网络返回或 Manager cleanup 关闭连接来结束在途调用。
