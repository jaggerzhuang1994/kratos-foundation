package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// consumerOperations 收拢 Redis Consumer 使用的网络命令，使真实 Consume 生命周期可在
// 不启动 Redis 服务的情况下验证，同时保持公共构造与队列接口不变。
type consumerOperations interface {
	createGroup(context.Context, string, string, string) error
	readGroup(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error)
	autoClaim(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error)
	acknowledge(context.Context, string, string, ...string) error
	refreshClaim(context.Context, *goredis.XClaimArgs) ([]string, error)
}

type redisConsumerOperations struct {
	client *goredis.Client
}

func (o *redisConsumerOperations) createGroup(
	ctx context.Context,
	stream string,
	group string,
	start string,
) error {
	return o.client.XGroupCreateMkStream(ctx, stream, group, start).Err()
}

func (o *redisConsumerOperations) readGroup(
	ctx context.Context,
	args *goredis.XReadGroupArgs,
) ([]goredis.XStream, error) {
	return o.client.XReadGroup(ctx, args).Result()
}

func (o *redisConsumerOperations) autoClaim(
	ctx context.Context,
	args *goredis.XAutoClaimArgs,
) ([]goredis.XMessage, string, error) {
	return o.client.XAutoClaim(ctx, args).Result()
}

func (o *redisConsumerOperations) acknowledge(
	ctx context.Context,
	stream string,
	group string,
	ids ...string,
) error {
	return o.client.XAck(ctx, stream, group, ids...).Err()
}

func (o *redisConsumerOperations) refreshClaim(
	ctx context.Context,
	args *goredis.XClaimArgs,
) ([]string, error) {
	return o.client.XClaimJustID(ctx, args).Result()
}

var _ consumerOperations = (*redisConsumerOperations)(nil)
