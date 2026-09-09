package redis

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestConsumerRecoversRedisOperationsWithoutReplayingHandler(t *testing.T) {
	for _, stage := range []string{"group", "read", "claim", "ack"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				attempts, handled := 0, 0
				operations := reconnectOperations(cancel)
				fail := func() error {
					attempts++
					if attempts <= 3 {
						return io.EOF
					}
					return nil
				}
				switch stage {
				case "group":
					operations.createGroupFn = func(context.Context, string, string, string) error { return fail() }
				case "read":
					operations.readGroupFn = func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
						if err := fail(); err != nil {
							return nil, err
						}
						return reconnectMessage(), nil
					}
				case "claim":
					operations.autoClaimFn = func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
						return nil, "0-0", fail()
					}
				case "ack":
					operations.acknowledgeFn = func(context.Context, string, string, ...string) error {
						if err := fail(); err != nil {
							return err
						}
						cancel()
						return nil
					}
				}
				started := time.Now()
				err := newConsumer(lifecycleConsumerConfig(1), nil, operations).Consume(ctx, func(context.Context, queue.Delivery) error { handled++; return nil })
				if !errors.Is(err, context.Canceled) || handled != 1 || attempts != 4 {
					t.Fatalf("Consume=%v, handled=%d attempts=%d; want recovery and one handler", err, handled, attempts)
				}
				if time.Since(started) < 560*time.Millisecond {
					t.Fatalf("retries did not back off progressively: %s", time.Since(started))
				}
			})
		})
	}
}

func TestConsumerRecoversMissingGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	operations := reconnectOperations(cancel)
	var groupStarts []string
	reads := 0
	operations.createGroupFn = func(_ context.Context, _, _, start string) error {
		groupStarts = append(groupStarts, start)
		if len(groupStarts) == 1 {
			return redisTestError("BUSYGROUP existing group")
		}
		return nil
	}
	operations.readGroupFn = func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
		reads++
		if reads == 1 {
			return nil, redisTestError("NOGROUP group disappeared")
		}
		return reconnectMessage(), nil
	}
	config := lifecycleConsumerConfig(1)
	config.StartPosition = queue.StartLatest
	err := newConsumer(config, nil, operations).Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	if !errors.Is(err, context.Canceled) || reads != 2 {
		t.Fatalf("Consume=%v reads=%d", err, reads)
	}
	if len(groupStarts) != 2 || groupStarts[0] != "$" || groupStarts[1] != "0" {
		t.Fatalf("group starts=%v; recovery must preserve the stream backlog", groupStarts)
	}
}

func TestConsumerDoesNotReconnectHandlerNetworkErrors(t *testing.T) {
	operations := reconnectOperations(func() { t.Error("handler failure was ACKed") })
	handled := 0
	err := newConsumer(lifecycleConsumerConfig(1), nil, operations).Consume(context.Background(), func(context.Context, queue.Delivery) error { handled++; return io.EOF })
	if !errors.Is(err, io.EOF) || handled != 1 {
		t.Fatalf("Consume=%v handled=%d", err, handled)
	}
}

func TestConsumerReconnectWaitHonorsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		operations := reconnectOperations(func() {})
		operations.createGroupFn = func(context.Context, string, string, string) error { return io.EOF }
		started := time.Now()
		err := newConsumer(lifecycleConsumerConfig(1), nil, operations).Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 50*time.Millisecond {
			t.Fatalf("Consume=%v after %s", err, time.Since(started))
		}
	})
}

func reconnectMessage() []goredis.XStream {
	return []goredis.XStream{{Messages: []goredis.XMessage{{ID: "1-0", Values: map[string]any{fieldID: "message-1", fieldHeaders: "[]", fieldTimestamp: ""}}}}}
}
func reconnectOperations(cancel func()) *consumerOperationsStub {
	return &consumerOperationsStub{
		createGroupFn: func(context.Context, string, string, string) error { return nil },
		autoClaimFn: func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
			return nil, "0-0", nil
		},
		readGroupFn: func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
			return reconnectMessage(), nil
		},
		acknowledgeFn:  func(context.Context, string, string, ...string) error { cancel(); return nil },
		refreshClaimFn: func(_ context.Context, args *goredis.XClaimArgs) ([]string, error) { return args.Messages, nil },
	}
}

type redisTestError string

func (e redisTestError) Error() string { return string(e) }
func (e redisTestError) RedisError()   {}

func TestConsumerRecoversHeartbeatFailureThroughPending(t *testing.T) {
	for _, failure := range []error{io.EOF, redisTestError("NOGROUP missing group")} {
		t.Run(failure.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				operations := reconnectOperations(cancel)
				var groupStarts []string
				operations.createGroupFn = func(_ context.Context, _, _, start string) error {
					groupStarts = append(groupStarts, start)
					return nil
				}
				heartbeatFailed := make(chan struct{})
				reads, claims, handled, acks := 0, 0, 0, 0
				operations.readGroupFn = func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
					reads++
					if reads == 1 {
						return reconnectMessage(), nil
					}
					time.Sleep(50 * time.Millisecond)
					return nil, goredis.Nil
				}
				operations.autoClaimFn = func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
					claims++
					if claims > 1 {
						return reconnectMessage()[0].Messages, "0-0", nil
					}
					return nil, "0-0", nil
				}
				operations.refreshClaimFn = func(context.Context, *goredis.XClaimArgs) ([]string, error) {
					close(heartbeatFailed)
					return nil, failure
				}
				operations.acknowledgeFn = func(context.Context, string, string, ...string) error { acks++; cancel(); return nil }
				config := lifecycleConsumerConfig(1)
				config.StartPosition = queue.StartLatest
				err := newConsumer(config, nil, operations).Consume(ctx, func(context.Context, queue.Delivery) error {
					handled++
					if handled == 1 {
						<-heartbeatFailed
					}
					return nil
				})
				if goredis.HasErrorPrefix(failure, "NOGROUP") && (len(groupStarts) != 2 || groupStarts[0] != "$" || groupStarts[1] != "0") {
					t.Fatalf("heartbeat group starts=%v; recovery must preserve the stream backlog", groupStarts)
				}
				if !errors.Is(err, context.Canceled) || handled != 2 || acks != 1 || claims < 2 {
					t.Fatalf("Consume=%v handled=%d acks=%d claims=%d", err, handled, acks, claims)
				}
			})
		})
	}
}

func TestConsumerReconnectRejectsPermanentRedisErrors(t *testing.T) {
	for _, failure := range []error{redisTestError("NOAUTH credentials required"), redisTestError("WRONGTYPE unexpected data"), goredis.ErrClosed} {
		t.Run(failure.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				operations := reconnectOperations(func() {})
				operations.createGroupFn = func(context.Context, string, string, string) error { return failure }
				started := time.Now()
				err := newConsumer(lifecycleConsumerConfig(1), nil, operations).Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
				if !errors.Is(err, failure) || time.Since(started) != 0 {
					t.Fatalf("Consume=%v after %s; permanent failure must return immediately", err, time.Since(started))
				}
			})
		})
	}
}
