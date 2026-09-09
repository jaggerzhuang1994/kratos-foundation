package redis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestConsumerConsumeRunsRedisDeliveryThroughHeartbeatCleanupBeforeAck(t *testing.T) {
	config := lifecycleConsumerConfig(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	heartbeatStarted := make(chan struct{})
	heartbeatFinished := make(chan struct{})
	var heartbeatOnce sync.Once
	var mu sync.Mutex
	var groupStart string
	var readConsumer string
	var claimConsumer string
	acknowledged := false
	operations := &consumerOperationsStub{
		createGroupFn: func(_ context.Context, stream, group, start string) error {
			if stream != "orders" || group != "billing" {
				return fmt.Errorf("unexpected group %s/%s", stream, group)
			}
			mu.Lock()
			groupStart = start
			mu.Unlock()
			return nil
		},
		autoClaimFn: func(_ context.Context, args *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
			mu.Lock()
			claimConsumer = args.Consumer
			mu.Unlock()
			return nil, "0-0", nil
		},
		readGroupFn: func(_ context.Context, args *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
			mu.Lock()
			readConsumer = args.Consumer
			mu.Unlock()
			return []goredis.XStream{{
				Stream: "orders",
				Messages: []goredis.XMessage{{
					ID: "1-0",
					Values: map[string]any{
						fieldID:        "message-1",
						fieldKey:       "order-1",
						fieldBody:      "payload",
						fieldHeaders:   "[]",
						fieldTimestamp: "",
					},
				}},
			}}, nil
		},
		refreshClaimFn: func(heartbeatCtx context.Context, args *goredis.XClaimArgs) ([]string, error) {
			if args.Consumer != "billing" || len(args.Messages) != 1 || args.Messages[0] != "1-0" {
				return nil, fmt.Errorf("unexpected heartbeat args %#v", args)
			}
			heartbeatOnce.Do(func() { close(heartbeatStarted) })
			<-heartbeatCtx.Done()
			close(heartbeatFinished)
			return nil, fmt.Errorf("heartbeat stopped: %w", heartbeatCtx.Err())
		},
		acknowledgeFn: func(_ context.Context, stream, group string, ids ...string) error {
			select {
			case <-heartbeatFinished:
			default:
				return errors.New("XACK ran before heartbeat cleanup")
			}
			if stream != "orders" || group != "billing" || len(ids) != 1 || ids[0] != "1-0" {
				return fmt.Errorf("unexpected XACK %s/%s/%v", stream, group, ids)
			}
			mu.Lock()
			acknowledged = true
			mu.Unlock()
			cancel()
			return nil
		},
	}
	consumer := newConsumer(config, nil, operations)
	handled := 0
	err := consumer.Consume(ctx, func(_ context.Context, delivery queue.Delivery) error {
		handled++
		if delivery.Err != nil || delivery.Message == nil || delivery.Message.ID != "message-1" ||
			string(delivery.Message.Body) != "payload" {
			t.Fatalf("delivery = %#v", delivery)
		}
		select {
		case <-heartbeatStarted:
		case <-time.After(time.Second):
			t.Fatal("heartbeat did not start")
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume error = %v, want cancellation after ACK", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if handled != 1 || !acknowledged || groupStart != "0" ||
		claimConsumer != "billing" || readConsumer != "billing" {
		t.Fatalf(
			"handled=%d ack=%v start=%q claim=%q read=%q",
			handled,
			acknowledged,
			groupStart,
			claimConsumer,
			readConsumer,
		)
	}
}

func TestConsumerConsumeDoesNotAckHandlerOrHeartbeatFailure(t *testing.T) {
	handlerErr := errors.New("handler failed")
	heartbeatErr := errors.New("heartbeat failed")
	tests := []struct {
		name      string
		handler   func(<-chan struct{}) queue.DeliveryHandler
		heartbeat func(context.Context, *goredis.XClaimArgs) ([]string, error)
		wantErr   error
	}{
		{
			name: "handler",
			handler: func(<-chan struct{}) queue.DeliveryHandler {
				return func(context.Context, queue.Delivery) error { return handlerErr }
			},
			heartbeat: func(context.Context, *goredis.XClaimArgs) ([]string, error) {
				return nil, errors.New("unexpected heartbeat")
			},
			wantErr: handlerErr,
		},
		{
			name: "heartbeat",
			handler: func(started <-chan struct{}) queue.DeliveryHandler {
				return func(context.Context, queue.Delivery) error {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("heartbeat did not run")
					}
					return nil
				}
			},
			heartbeat: func(context.Context, *goredis.XClaimArgs) ([]string, error) {
				return nil, heartbeatErr
			},
			wantErr: heartbeatErr,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			heartbeatStarted := make(chan struct{})
			var heartbeatOnce sync.Once
			acknowledged := false
			operations := &consumerOperationsStub{
				createGroupFn: func(context.Context, string, string, string) error { return nil },
				autoClaimFn: func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
					return nil, "0-0", nil
				},
				readGroupFn: func(context.Context, *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
					return []goredis.XStream{{Messages: []goredis.XMessage{{
						ID: "1-0",
						Values: map[string]any{
							fieldID:        "message-1",
							fieldHeaders:   "[]",
							fieldTimestamp: "",
						},
					}}}}, nil
				},
				refreshClaimFn: func(ctx context.Context, args *goredis.XClaimArgs) ([]string, error) {
					heartbeatOnce.Do(func() { close(heartbeatStarted) })
					return test.heartbeat(ctx, args)
				},
				acknowledgeFn: func(context.Context, string, string, ...string) error {
					acknowledged = true
					return nil
				},
			}
			consumer := newConsumer(lifecycleConsumerConfig(1), nil, operations)
			err := consumer.Consume(context.Background(), test.handler(heartbeatStarted))
			if !errors.Is(err, test.wantErr) || acknowledged {
				t.Fatalf("Consume error=%v acknowledged=%v", err, acknowledged)
			}
		})
	}
}

func TestConsumerConsumeCancelsSiblingRedisInstanceAfterReadFailure(t *testing.T) {
	wantErr := errors.New("redis read failed")
	allReadsStarted := make(chan struct{})
	var allReadsOnce sync.Once
	var mu sync.Mutex
	var readers []string
	acknowledged := false
	operations := &consumerOperationsStub{
		createGroupFn: func(context.Context, string, string, string) error { return nil },
		autoClaimFn: func(context.Context, *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
			return nil, "0-0", nil
		},
		readGroupFn: func(ctx context.Context, args *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
			mu.Lock()
			readers = append(readers, args.Consumer)
			if len(readers) == 2 {
				allReadsOnce.Do(func() { close(allReadsStarted) })
			}
			mu.Unlock()
			select {
			case <-allReadsStarted:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if args.Consumer == "billing-1" {
				return nil, wantErr
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		acknowledgeFn: func(context.Context, string, string, ...string) error {
			acknowledged = true
			return nil
		},
	}
	consumer := newConsumer(lifecycleConsumerConfig(2), nil, operations)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := consumer.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	if !errors.Is(err, wantErr) || acknowledged {
		t.Fatalf("Consume error=%v acknowledged=%v", err, acknowledged)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(readers)
	if len(readers) != 2 || readers[0] != "billing-1" || readers[1] != "billing-2" {
		t.Fatalf("readers = %#v, want billing-1 and billing-2", readers)
	}
}
