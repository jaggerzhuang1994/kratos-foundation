// Package redis 提供 Redis 订阅功能的封装
package redis

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/redis/go-redis/v9"
)

// subscribeRdb 定义 Redis 订阅接口
type subscribeRdb interface {
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// Subscribe 封装 Redis 订阅逻辑，并将消息解析为指定类型
// 使用泛型支持自定义消息类型
// 返回消息只读通道和错误只读通道
// 当 context 被取消或订阅关闭时，通道会自动关闭
// parser 函数用于将 Redis 消息解析为目标类型 T
func Subscribe[T any](rdb subscribeRdb, ctx context.Context, channel string, parser func(message *Message) (T, error), options ...SubscribeOption) (<-chan T, <-chan error) {
	opt := &subscribeOption{
		chSize:    100, // 默认消息通道大小
		errChSize: 10,  // 默认错误通道大小
	}
	for _, fn := range options {
		fn(opt)
	}
	ch := make(chan T, opt.chSize)
	errCh := make(chan error, opt.errChSize)
	go func() {
		defer close(ch)
		defer close(errCh)

		sub := rdb.Subscribe(ctx, channel)
		defer func() {
			err := sub.Close()
			if err != nil {
				errCh <- err
			}
		}()

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
						if r := recover(); r != nil {
							errCh <- fmt.Errorf("handle redis message panic: %v\n%s\n", r, debug.Stack())
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
