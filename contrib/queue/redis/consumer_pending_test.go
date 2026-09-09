package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

// 每条 Handler 都有正常心跳；等待执行的消息不能提前进入本 worker 的 Pending。
func TestConsumerSlowHandlerDoesNotExposePrefetchedPending(t *testing.T) {
	for _, source := range []string{"new messages", "pending messages"} {
		t.Run(source, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				config, err := normalizeConsumerConfig(ConsumerConfig{
					Connection: "main", Stream: "orders", Group: "billing", Instance: "first",
					ClaimIdle: 300 * time.Millisecond, BlockTimeout: 10 * time.Millisecond,
				})
				if err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				ids := []string{"1-0", "2-0"}
				pending := make(map[string]time.Time)
				unread := append([]string(nil), ids...)
				if source == "pending messages" {
					unread = nil
					for _, id := range ids {
						pending[id] = time.Now().Add(-time.Second)
					}
				}
				handled := make(map[string]int)
				acknowledged := make(map[string]bool)
				operations := &consumerOperationsStub{
					createGroupFn: func(context.Context, string, string, string) error { return nil },
					readGroupFn: func(_ context.Context, args *goredis.XReadGroupArgs) ([]goredis.XStream, error) {
						mu.Lock()
						var messages []goredis.XMessage
						for len(unread) > 0 && int64(len(messages)) < args.Count {
							id := unread[0]
							unread = unread[1:]
							pending[id] = time.Now()
							messages = append(messages, goredis.XMessage{ID: id})
						}
						mu.Unlock()
						if len(messages) == 0 {
							time.Sleep(args.Block)
							return nil, goredis.Nil
						}
						return []goredis.XStream{{Messages: messages}}, nil
					},
					autoClaimFn: func(_ context.Context, args *goredis.XAutoClaimArgs) ([]goredis.XMessage, string, error) {
						mu.Lock()
						defer mu.Unlock()
						var messages []goredis.XMessage
						for _, id := range ids {
							if idleSince, ok := pending[id]; ok && time.Since(idleSince) >= args.MinIdle && int64(len(messages)) < args.Count {
								pending[id] = time.Now()
								messages = append(messages, goredis.XMessage{ID: id})
							}
						}
						return messages, "0-0", nil
					},
					refreshClaimFn: func(_ context.Context, args *goredis.XClaimArgs) ([]string, error) {
						mu.Lock()
						defer mu.Unlock()
						id := args.Messages[0]
						if _, ok := pending[id]; !ok {
							return nil, nil
						}
						pending[id] = time.Now()
						return []string{id}, nil
					},
					acknowledgeFn: func(_ context.Context, _, _ string, ids ...string) error {
						mu.Lock()
						defer mu.Unlock()
						for _, id := range ids {
							delete(pending, id)
							acknowledged[id] = true
						}
						if len(acknowledged) == 2 {
							cancel()
						}
						return nil
					},
				}
				handler := func(_ context.Context, delivery queue.Delivery) error {
					mu.Lock()
					handled[delivery.Message.ID]++
					mu.Unlock()
					time.Sleep(500 * time.Millisecond)
					return nil
				}
				first := newConsumer(config, nil, operations)
				second := newConsumer(config, nil, operations).(*consumer)
				secondDone := make(chan error, 1)
				go func() {
					time.Sleep(350 * time.Millisecond)
					messages, _, err := operations.autoClaim(ctx, &goredis.XAutoClaimArgs{
						Consumer: "second", Start: "0-0", MinIdle: config.ClaimIdle, Count: 1,
					})
					if err == nil {
						err = second.process(ctx, "second", messages, handler)
					}
					secondDone <- err
				}()
				err = first.Consume(ctx, handler)
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Consume = %v", err)
				}
				if err := <-secondDone; err != nil {
					t.Errorf("second consumer = %v", err)
				}
				if handled["1-0"] != 1 || handled["2-0"] != 1 || len(acknowledged) != 2 {
					t.Fatalf("handled=%v acknowledged=%v; each message should run once", handled, acknowledged)
				}
			})
		})
	}
}
