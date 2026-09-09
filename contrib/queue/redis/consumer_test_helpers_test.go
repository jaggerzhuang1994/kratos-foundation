package redis

import (
	"context"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

type cancelContextKey struct{}

type recordingManager struct {
	client      *goredis.Client
	err         error
	connections []string
}

func lifecycleConsumerConfig(concurrency int) ConsumerConfig {
	return ConsumerConfig{
		Connection:    "main",
		Stream:        "orders",
		Group:         "billing",
		Instance:      "billing",
		Concurrency:   concurrency,
		StartPosition: queue.StartEarliest,
		BlockTimeout:  time.Millisecond,
		ClaimIdle:     300 * time.Millisecond,
	}
}

type consumerOperationsStub struct {
	createGroupFn  func(context.Context, string, string, string) error
	readGroupFn    func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error)
	autoClaimFn    func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error)
	acknowledgeFn  func(context.Context, string, string, ...string) error
	refreshClaimFn func(context.Context, *goredis.XClaimArgs) ([]string, error)
}

func (o *consumerOperationsStub) createGroup(
	ctx context.Context,
	stream string,
	group string,
	start string,
) error {
	return o.createGroupFn(ctx, stream, group, start)
}

func (o *consumerOperationsStub) readGroup(
	ctx context.Context,
	args *goredis.XReadGroupArgs,
) ([]goredis.XStream, error) {
	return o.readGroupFn(ctx, args)
}

func (o *consumerOperationsStub) autoClaim(
	ctx context.Context,
	args *goredis.XAutoClaimArgs,
) ([]goredis.XMessage, string, error) {
	return o.autoClaimFn(ctx, args)
}

func (o *consumerOperationsStub) acknowledge(
	ctx context.Context,
	stream string,
	group string,
	ids ...string,
) error {
	return o.acknowledgeFn(ctx, stream, group, ids...)
}

func (o *consumerOperationsStub) refreshClaim(
	ctx context.Context,
	args *goredis.XClaimArgs,
) ([]string, error) {
	return o.refreshClaimFn(ctx, args)
}

func (m *recordingManager) Default() *goredis.Client { return m.client }

func (m *recordingManager) Connection(name string) (*goredis.Client, error) {
	m.connections = append(m.connections, name)
	return m.client, m.err
}
