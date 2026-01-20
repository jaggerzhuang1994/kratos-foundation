package concurrent_policy

import (
	"context"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/internal/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

func Middleware(log log.Log, policy config_pb.ConcurrentPolicy) middleware.Middleware {
	switch policy {
	case config_pb.ConcurrentPolicy_DELAY:
		return delayIfStillRunning(log)
	case config_pb.ConcurrentPolicy_SKIP:
		return skipIfStillRunning(log)
	}
	return nil
}

func delayIfStillRunning(log log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		var mu sync.Mutex
		return func(ctx context.Context) error {
			start := time.Now()
			mu.Lock()
			defer mu.Unlock()
			// 如果 ctx 已经结束，则退出。 避免堆积很多 delay 任务不能清空
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if dur := time.Since(start); dur > 5*time.Second {
				log.WithContext(ctx).With("duration", dur).Warn("delay")
			}
			return next(ctx)
		}
	}
}

func skipIfStillRunning(log log.Log) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		var ch = make(chan struct{}, 1)
		ch <- struct{}{}
		return func(ctx context.Context) (err error) {
			select {
			case v := <-ch:
				err = next(ctx)
				ch <- v
			default:
				log.WithContext(ctx).Warn("skip")
			}
			return
		}
	}
}
