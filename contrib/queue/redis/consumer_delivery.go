package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

// process 顺序处理一批 Redis 消息，仅在 Handler 与心跳都成功后 ACK。
func (c *consumer) process(
	ctx context.Context,
	instanceName string,
	messages []goredis.XMessage,
	handler queue.DeliveryHandler,
) error {
	for _, raw := range messages {
		message, err := decodeMessage(raw)
		if err != nil {
			err = fmt.Errorf(
				"decode redis stream message %q from %q: %w",
				raw.ID,
				c.config.Stream,
				err,
			)
			message = malformedMessage(raw)
		}
		stopHeartbeat := c.keepAlive(ctx, instanceName, raw.ID)
		var handlerErr, heartbeatErr error
		if err := processDelivery(
			ctx,
			queue.Delivery{Message: message, Err: err},
			func(ctx context.Context, delivery queue.Delivery) error {
				handlerErr = handler(ctx, delivery)
				return handlerErr
			},
			func() error { heartbeatErr = stopHeartbeat(); return heartbeatErr },
			func() error {
				if err := c.retryOperation(ctx, restoreMissingGroup, func() error {
					return c.operations.acknowledge(ctx, c.config.Stream, c.config.Group, raw.ID)
				}); err != nil {
					return fmt.Errorf(
						"redis XACK message %q from %q: %w",
						raw.ID,
						c.config.Stream,
						err,
					)
				}
				return nil
			},
		); err != nil {
			// 失联后不再原地刷新 MinIdle=0 的 claim；保留 Pending，由正常回收流程
			// 决定下一次交付。业务错误优先，绝不因其具有网络错误形状而重放。
			missingGroup := goredis.HasErrorPrefix(heartbeatErr, "NOGROUP")
			if handlerErr == nil && ctx.Err() == nil && (transientRedisError(heartbeatErr) || missingGroup) {
				if missingGroup {
					if err := c.createGroup(ctx, "0"); err != nil {
						return err
					}
				}
				if c.log != nil {
					c.log.With("error", heartbeatErr).Warn("redis queue heartbeat interrupted; leaving message pending")
				}
				if waitErr := (reconnect.Backoff{}).Wait(ctx, 0); waitErr != nil {
					return waitErr
				}
				continue
			}
			return err
		}
	}
	return nil
}

// processDelivery 将 Redis 投递协议固定为 Handler、心跳清理、XACK 的顺序。
// Handler、心跳清理或 Context 失败时不会 ACK，返回值保留每个原始错误的 errors.Is 链。
func processDelivery(
	ctx context.Context,
	delivery queue.Delivery,
	handler queue.DeliveryHandler,
	cleanupHeartbeat func() error,
	acknowledge func() error,
) error {
	if ctx == nil {
		return errors.New("redis delivery context is nil")
	}
	if handler == nil {
		return errors.New("redis queue delivery handler is nil")
	}
	if cleanupHeartbeat == nil {
		return errors.New("redis queue heartbeat cleanup is nil")
	}
	if acknowledge == nil {
		return errors.New("redis queue acknowledge function is nil")
	}
	err := runWithCleanup(
		func() error { return handler(ctx, delivery) },
		cleanupHeartbeat,
	)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return err
	}
	return acknowledge()
}

// runWithCleanup 无论 Handler 返回还是 panic 都执行心跳清理，正常返回时合并两个错误。
func runWithCleanup(run func() error, cleanup func() error) (err error) {
	defer func() {
		err = errors.Join(err, cleanup())
	}()
	return run()
}

// keepAlive 定期刷新 Pending 消息的闲置时间，避免长 Handler 被其他实例重复回收。
func (c *consumer) keepAlive(
	ctx context.Context,
	instanceName string,
	messageID string,
) func() error {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(heartbeatInterval(c.config.ClaimIdle))
		defer ticker.Stop()
		done <- runHeartbeat(
			heartbeatCtx,
			ticker.C,
			messageID,
			func(ctx context.Context) ([]string, error) {
				return c.operations.refreshClaim(
					ctx,
					&goredis.XClaimArgs{
						Stream:   c.config.Stream,
						Group:    c.config.Group,
						Consumer: instanceName,
						MinIdle:  0,
						Messages: []string{messageID},
					},
				)
			},
		)
	}()
	return func() error {
		cancel()
		return <-done
	}
}

// runHeartbeat 在每次 tick 刷新 Pending claim。cleanup 与在途 Redis 请求并发时，
// 只忽略能通过 errors.Is 明确匹配心跳 Context 取消原因的错误；其他后端错误必须阻止 XACK。
func runHeartbeat(
	ctx context.Context,
	ticks <-chan time.Time,
	messageID string,
	claim func(context.Context) ([]string, error),
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			claimed, err := claim(ctx)
			if err != nil {
				if contextErr := ctx.Err(); contextErr != nil && errors.Is(err, contextErr) {
					return nil
				}
				return fmt.Errorf(
					"refresh Redis queue message %q claim: %w",
					messageID,
					err,
				)
			}
			if len(claimed) != 1 || claimed[0] != messageID {
				return fmt.Errorf(
					"redis queue message %q is no longer pending",
					messageID,
				)
			}
		}
	}
}

// heartbeatInterval 计算心跳周期，使正常 Handler 完成前至少能刷新两次闲置时间。
func heartbeatInterval(idle time.Duration) time.Duration {
	interval := idle / 3
	if interval < minimumHeartbeatInterval {
		return minimumHeartbeatInterval
	}
	return interval
}
