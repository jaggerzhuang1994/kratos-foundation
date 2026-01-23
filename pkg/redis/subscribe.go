// Package redis 提供 Redis 订阅功能的封装
package redis

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/redis/go-redis/v9"
)

// subscribeRdb 定义 Redis 订阅接口
// 该接口抽象了 Redis 客户端的订阅功能，便于单元测试和替换实现
type subscribeRdb interface {
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// Subscribe 封装 Redis 订阅逻辑，并将消息解析为指定类型
//
// 泛型参数：
//   - T: 目标消息类型，可以是任意结构体或基本类型
//
// 参数：
//   - rdb: Redis 客户端，实现了 subscribeRdb 接口
//   - ctx: 上下文，用于控制订阅生命周期和取消订阅
//   - channel: 要订阅的 Redis 频道名称
//   - parser: 消息解析函数，将 Redis 消息转换为目标类型 T
//   - options: 订阅选项函数，用于自定义通道大小等配置
//
// 返回：
//   - <-chan T: 消息只读通道，接收解析后的消息
//   - <-chan error: 错误只读通道，接收解析和订阅过程中的错误
//
// 功能特性：
//   - 自动管理订阅生命周期，context 取消时自动关闭订阅
//   - 消息解析异常会被捕获并转为错误发送到错误通道
//   - 支持通过 options 自定义消息和错误通道的缓冲大小
//
// 使用建议：
//   - 务必从两个通道中读取数据，否则可能导致 goroutine 阻塞
//   - 建议在单独的 goroutine 中处理消息和错误
//   - context 取消后，两个通道会自动关闭
//
// 注意事项：
//   - 如果 parser 函数触发 panic，会被捕获并转换为错误
//   - 订阅关闭时的错误也会发送到错误通道
//   - 通道缓冲大小应根据消息频率合理配置
//
// 示例：
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	msgCh, errCh := redis.Subscribe[MyMessage](rdb, ctx, "my-channel", func(msg *redis.Message) (MyMessage, error) {
//	    var m MyMessage
//	    err := json.Unmarshal([]byte(msg.Payload), &m)
//	    return m, err
//	})
//
//	for {
//	    select {
//	    case msg, ok := <-msgCh:
//	        if !ok { return }
//	        fmt.Println("Received:", msg)
//	    case err, ok := <-errCh:
//	        if !ok { return }
//	        log.Printf("Error: %v", err)
//	    }
//	}
func Subscribe[T any](rdb subscribeRdb, ctx context.Context, channel string, parser func(message *Message) (T, error), options ...SubscribeOption) (<-chan T, <-chan error) {
	// 应用默认配置和用户自定义配置
	opt := &subscribeOption{
		chSize:    100, // 默认消息通道大小
		errChSize: 10,  // 默认错误通道大小
	}
	for _, fn := range options {
		fn(opt)
	}

	// 创建带缓冲的消息和错误通道
	ch := make(chan T, opt.chSize)
	errCh := make(chan error, opt.errChSize)

	// 启动订阅 goroutine
	go func() {
		// 确保 goroutine 退出时关闭通道
		defer close(ch)
		defer close(errCh)

		// 订阅 Redis 频道
		sub := rdb.Subscribe(ctx, channel)
		defer func() {
			// 关闭订阅时，将错误发送到错误通道
			err := sub.Close()
			if err != nil {
				errCh <- err
			}
		}()

		// 获取 Redis 消息通道
		recCh := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				// context 被取消，退出订阅
				return
			case msg, ok := <-recCh:
				if !ok {
					// Redis 消息通道关闭，退出订阅
					return
				}

				// 在独立的匿名函数中处理消息，确保 panic 能被正确捕获
				func() {
					defer func() {
						if r := recover(); r != nil {
							// 捕获 parser 中的 panic，转换为错误并发送到错误通道
							errCh <- fmt.Errorf("handle redis message panic: %v\n%s\n", r, debug.Stack())
						}
					}()

					// 使用用户提供的 parser 解析消息
					t, err := parser(msg)
					if err != nil {
						// 解析失败，发送错误到错误通道
						errCh <- err
					} else {
						// 解析成功，发送消息到消息通道
						ch <- t
					}
				}()
			}
		}
	}()

	return ch, errCh
}
