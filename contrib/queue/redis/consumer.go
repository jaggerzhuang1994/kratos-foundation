package redis

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

type consumer struct {
	operations consumerOperations
	config     ConsumerConfig
	log        log.Logger
	running    atomic.Bool
}

var _ queue.Consumer = (*consumer)(nil)

// newConsumer 只注入 Redis 网络操作；生产路径仍由 NewConsumer 解析并借用 Manager Client。
func newConsumer(
	config ConsumerConfig,
	logger log.Logger,
	operations consumerOperations,
) queue.Consumer {
	if logger != nil {
		logger = logger.WithModule("queue.redis")
	}
	return &consumer{operations: operations, config: config, log: logger}
}

// Consume 同时交付消息与解码错误，使 ConsumerRuntime 能统一执行失败和死信策略。
func (c *consumer) Consume(
	ctx context.Context,
	handler queue.DeliveryHandler,
) error {
	if handler == nil {
		return fmt.Errorf("redis queue delivery handler is nil")
	}
	if c == nil || c.operations == nil {
		return errors.New("redis queue consumer is not initialized")
	}
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("redis queue consumer is already running")
	}
	defer c.running.Store(false)
	return c.consume(ctx, handler)
}

// consume 运行 Delivery 消费循环，入口已负责生命周期互斥。
func (c *consumer) consume(
	ctx context.Context,
	handler queue.DeliveryHandler,
) error {
	if err := c.ensureGroup(ctx); err != nil {
		return err
	}
	if c.log != nil {
		c.log.Debugw(
			"msg", "redis queue consumer group ready",
			"queue.destination", c.config.Stream,
			"queue.group", c.config.Group,
		)
	}

	group, groupCtx := errgroup.WithContext(ctx)
	for index := 0; index < c.config.Concurrency; index++ {
		instanceName := c.config.instanceName(index)
		group.Go(func() error {
			return c.consumeInstance(groupCtx, instanceName, handler)
		})
	}
	return group.Wait()
}

// ensureGroup 幂等创建 Redis Consumer Group，已存在时视为成功。
func (c *consumer) ensureGroup(ctx context.Context) error {
	start := "0"
	if c.config.StartPosition == queue.StartLatest {
		start = "$"
	}
	return c.createGroup(ctx, start)
}

// createGroup 幂等创建消费组；丢组恢复从 0 开始，避免跳过断线期间保留的消息。
func (c *consumer) createGroup(ctx context.Context, start string) error {
	err := c.retryOperation(ctx, noGroupRecovery, func() error {
		err := c.operations.createGroup(ctx, c.config.Stream, c.config.Group, start)
		if goredis.HasErrorPrefix(err, "BUSYGROUP") {
			return nil
		}
		return err
	})
	if err == nil {
		return nil
	}
	return fmt.Errorf(
		"create redis consumer group %q on %q: %w",
		c.config.Group,
		c.config.Stream,
		err,
	)
}

// consumeInstance 运行单个消费者实例，交替回收超时 Pending 消息与读取新消息。
func (c *consumer) consumeInstance(
	ctx context.Context,
	instanceName string,
	handler queue.DeliveryHandler,
) error {
	claimCursor := "0-0"
	nextClaim := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !time.Now().Before(nextClaim) {
			messages, next, err := c.claim(ctx, instanceName, claimCursor)
			if err != nil {
				return err
			}
			if err := c.process(ctx, instanceName, messages, handler); err != nil {
				return err
			}
			claimCursor = next
			if claimCursor == "0-0" {
				nextClaim = time.Now().Add(claimInterval(c.config.ClaimIdle))
			} else {
				continue
			}
		}

		// XREADGROUP 不应跨过下次 Pending 扫描时间，否则空队列会延迟故障消息的回收。
		// 串行 worker 每次只领取一条，避免批次中等待执行的消息无心跳而被其他实例回收。
		blockTimeout := min(c.config.BlockTimeout, time.Until(nextClaim))
		blockTimeout = max(blockTimeout, minimumBlockTimeout)
		var streams []goredis.XStream
		err := c.retryOperation(ctx, restoreMissingGroup, func() error {
			var readErr error
			streams, readErr = c.operations.readGroup(ctx, &goredis.XReadGroupArgs{
				Group:    c.config.Group,
				Consumer: instanceName,
				Streams:  []string{c.config.Stream, ">"},
				Count:    1,
				Block:    blockTimeout,
			})
			return readErr
		})
		if errors.Is(err, goredis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("redis XREADGROUP %q: %w", c.config.Stream, err)
		}
		for _, stream := range streams {
			if err := c.process(ctx, instanceName, stream.Messages, handler); err != nil {
				return err
			}
		}
	}
}

// claim 分页回收闲置超时的 Pending 消息，返回下一页游标。
func (c *consumer) claim(
	ctx context.Context,
	instanceName string,
	cursor string,
) ([]goredis.XMessage, string, error) {
	var messages []goredis.XMessage
	var next string
	err := c.retryOperation(ctx, restoreMissingGroup, func() error {
		var claimErr error
		messages, next, claimErr = c.operations.autoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   c.config.Stream,
			Group:    c.config.Group,
			Consumer: instanceName,
			MinIdle:  c.config.ClaimIdle,
			Start:    cursor,
			Count:    1,
		})
		return claimErr
	})
	if errors.Is(err, goredis.Nil) {
		return nil, "0-0", nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, "0-0", ctx.Err()
		}
		return nil, "0-0", fmt.Errorf(
			"redis XAUTOCLAIM %q: %w",
			c.config.Stream,
			err,
		)
	}
	return messages, next, nil
}

// claimInterval 计算 Pending 扫描周期，下限避免空队列上的忙循环。
func claimInterval(idle time.Duration) time.Duration {
	interval := idle / 2
	if interval < minimumClaimInterval {
		return minimumClaimInterval
	}
	return interval
}
