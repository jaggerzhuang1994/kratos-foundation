package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
)

// minClusterLockTTL 是集群锁租约的下限；过短的租约来不及续租，也吸收不了副本间的时钟偏差。
const minClusterLockTTL = time.Second

// errClusterLockLost 表示执行期间续租失败，任务 Context 以此为原因取消。
var errClusterLockLost = errors.New("job cluster lock lost")

// clusterLock 保存 WithClusterLock 的参数。
type clusterLock struct {
	// locker 提供分布式租约，通常为 contrib/lock/redis。
	locker lock.Locker
	// key 逻辑锁键；空值使用 "job:" + 任务名。
	key string
	// ttl 每次触发持有的租约时长，执行期间按 ttl/3 续租。
	ttl time.Duration
}

// WithClusterLock 让周期任务在共享同一 Locker 的所有实例中每次触发只执行一次：触发时先 TryLock，
// 拿到租约的实例执行，其余实例跳过本轮（job_triggers_skipped_total reason=cluster_locked）。
//
// 执行期间每 ttl/3 续租一次；续租失败说明租约已被他人取得，任务 Context 立即取消，避免两个实例同时执行。
// 执行结束后不主动释放，租约保留到 TTL 自然过期，用来吸收副本间的时钟偏差，防止另一实例在同一触发点
// 稍晚再执行一次。因此 ttl 应大于副本间可能的时钟偏差、小于调度间隔，且不小于 1s。
// key 为空时使用 "job:" + 任务名；多个应用共享同一 Redis 时应带上应用名，避免同名任务互相阻塞。
// 取锁出错（如 Redis 不可用）时本轮不执行并返回错误，交给 WithErrorHandler。
func WithClusterLock(locker lock.Locker, key string, ttl time.Duration) CronOption {
	return func(options *cronOptions) {
		options.clusterLock = &clusterLock{locker: locker, key: strings.TrimSpace(key), ttl: ttl}
	}
}

func (c *clusterLock) validate(name string) error {
	if c.locker == nil {
		return errors.New("cluster lock requires a locker")
	}
	if c.ttl < minClusterLockTTL {
		return fmt.Errorf("cluster lock ttl must be at least %s", minClusterLockTTL)
	}
	if c.key == "" {
		c.key = "job:" + name
	}
	return nil
}

func (c *clusterLock) middleware(log moduleLog, name string, metrics jobAdmissionMetrics) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context) error {
			lease, err := c.locker.TryLock(ctx, c.key, c.ttl)
			if errors.Is(err, lock.ErrNotAcquired) {
				// 其他实例已执行本轮，属于正常分工，只记指标与 Debug。
				metrics.reportSkipped(ctx, name, "cluster_locked")
				log.WithContext(ctx).With("event", "job.trigger.skipped", "job", name, "reason", "cluster_locked").Debug("job trigger skipped")
				return nil
			}
			if err != nil {
				return fmt.Errorf("acquire job cluster lock: %w", err)
			}
			runCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			done := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() { c.keepAlive(runCtx, cancel, done, lease, log, name) })
			err = next(runCtx)
			close(done)
			wg.Wait()
			if cause := context.Cause(runCtx); errors.Is(cause, errClusterLockLost) && ctx.Err() == nil {
				return errors.Join(err, cause)
			}
			return err
		}
	}
}

// keepAlive 在执行期间续租；续租失败时以 errClusterLockLost 取消任务 Context。
func (c *clusterLock) keepAlive(ctx context.Context, cancel context.CancelCauseFunc, done <-chan struct{}, lease lock.Lease, log moduleLog, name string) {
	ticker := time.NewTicker(c.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := lease.Refresh(ctx, c.ttl); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.WithContext(ctx).With("event", "job.cluster_lock.lost", "job", name, "error", err).Warn("job cluster lock lost; canceling run")
				cancel(errClusterLockLost)
				return
			}
		}
	}
}
