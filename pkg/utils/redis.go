package common

import (
	"context"
	"fmt"
	"runtime"

	"github.com/redis/go-redis/v9"
)

// RedisSubscribe 封装 Redis 发布订阅逻辑，并将消息解析为类型 T。
func RedisSubscribe[T any](rdb interface {
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}, ctx context.Context, channel string, parser func(message *redis.Message) (T, error)) (<-chan T, <-chan error) {
	ch := make(chan T)
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		defer close(ch)

		sub := rdb.Subscribe(ctx, channel)
		defer sub.Close()

		recCh := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-recCh:
				if !ok {
					return
				}
				func() {
					defer func() {
						if re := recover(); re != nil {
							buf := make([]byte, 64<<10) //nolint:mnd
							n := runtime.Stack(buf, false)
							buf = buf[:n]
							errCh <- fmt.Errorf("handle msg panic: %+v %s", re, buf)
						}
					}()
					t, err := parser(msg)
					if err != nil {
						errCh <- err
					} else {
						ch <- t
					}
				}()
			}
		}
	}()
	return ch, errCh
}
