package redis

import (
	"context"
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	goredis "github.com/redis/go-redis/v9"
)

type groupRecovery uint8

const (
	noGroupRecovery groupRecovery = iota
	restoreMissingGroup
)

// retryOperation 仅包围 Redis 网络操作，不能包围业务 Handler。每次成功或空读结束
// 本轮退避；Context 取消、关闭及永久协议错误直接返回，避免掩盖配置错误。
func (c *consumer) retryOperation(ctx context.Context, recovery groupRecovery, run func() error) error {
	backoff := reconnect.Backoff{}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := run()
		if err == nil || errors.Is(err, goredis.Nil) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if recovery == restoreMissingGroup && goredis.HasErrorPrefix(err, "NOGROUP") {
			if err := c.createGroup(ctx, "0"); err != nil {
				return err
			}
		} else if !transientRedisError(err) {
			return err
		}
		if c.log != nil {
			c.log.With("error", err, "retry", attempt+1).Warn("redis queue operation interrupted; reconnecting")
		}
		if err := backoff.Wait(ctx, attempt); err != nil {
			return err
		}
	}
}

func transientRedisError(err error) bool {
	if errors.Is(err, goredis.ErrClosed) {
		return false
	}
	if reconnect.Transient(err) || errors.Is(err, goredis.ErrPoolTimeout) {
		return true
	}
	for _, prefix := range []string{"LOADING", "TRYAGAIN", "CLUSTERDOWN", "MASTERDOWN", "READONLY"} {
		if goredis.HasErrorPrefix(err, prefix) {
			return true
		}
	}
	return false
}
