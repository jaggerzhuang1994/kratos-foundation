package redis

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const subscribeCloseEventTimeout = 100 * time.Millisecond

// Subscriber 是 Subscribe 所需的最小 Redis 能力；普通 Client 与 ClusterClient 均满足它。
type Subscriber interface {
	Subscribe(ctx context.Context, channels ...string) *goredis.PubSub
}

// SubscribeEvent 按接收顺序携带一条解析结果或处理错误。
type SubscribeEvent[T any] struct {
	// Message 是解析结果；Err 非 nil 时它可以是零值。
	Message T
	// Err 表示解析失败、解析器 panic 或订阅关闭失败。
	Err error
}

// Subscribe 在 Redis 确认订阅后返回单一有序事件流；取消 ctx 会关闭订阅和事件流。
// 调用方若关心关闭错误，需要持续读取到事件流关闭。
func Subscribe[T any](
	ctx context.Context,
	rdb Subscriber,
	channel string,
	parser func(message *goredis.Message) (T, error),
	options ...SubscribeOption,
) (<-chan SubscribeEvent[T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rdb == nil {
		return nil, errors.New("redis subscribe client is nil")
	}
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return nil, errors.New("redis subscribe channel is required")
	}
	if parser == nil {
		return nil, errors.New("redis subscribe parser is nil")
	}

	config := subscribeOption{bufferSize: defaultSubscribeBufferSize}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if config.bufferSize < 0 {
		return nil, fmt.Errorf(
			"redis subscribe buffer size cannot be negative: %d",
			config.bufferSize,
		)
	}

	pubSub := rdb.Subscribe(ctx, channel)
	if pubSub == nil {
		return nil, errors.New("redis subscribe client returned a nil subscription")
	}
	return startSubscription(
		ctx,
		pubSub,
		parser,
		config.bufferSize,
	)
}

type confirmedMessageSubscription interface {
	messageSubscription
	Receive(context.Context) (any, error)
}

// startSubscription 等待 Redis 确认后再启动消费，避免把“已发起”误报成“已订阅”。
func startSubscription[T any](
	ctx context.Context,
	subscription confirmedMessageSubscription,
	parser func(message *goredis.Message) (T, error),
	bufferSize int,
) (<-chan SubscribeEvent[T], error) {
	// Receive 的无期限读取不会被普通 cancel 打断；确认期间由取消回调关闭 socket。
	// 停止回调并等待已开始的关闭完成后，才把订阅所有权交给消费 goroutine。
	closed := make(chan error, 1)
	stopCancel := context.AfterFunc(ctx, func() { closed <- subscription.Close() })
	_, err := subscription.Receive(ctx)
	stopped := stopCancel()
	err = errors.Join(err, ctx.Err())
	var closeErr error
	if !stopped {
		closeErr = <-closed
	} else if err != nil {
		closeErr = subscription.Close()
	}
	if err != nil {
		receiveErr := fmt.Errorf(
			"confirm redis subscription: %w",
			err,
		)
		if closeErr != nil {
			return nil, errors.Join(
				receiveErr,
				fmt.Errorf(
					"close redis subscription after confirmation failure: %w",
					closeErr,
				),
			)
		}
		return nil, receiveErr
	}

	events := make(chan SubscribeEvent[T], bufferSize)
	go consumeSubscription(
		ctx,
		subscription,
		parser,
		events,
	)
	return events, nil
}

type messageSubscription interface {
	Channel(...goredis.ChannelOption) <-chan *goredis.Message
	Close() error
}

// consumeSubscription 串行解析消息并写入同一事件流，保留消息与错误的先后关系。
func consumeSubscription[T any](
	ctx context.Context,
	subscription messageSubscription,
	parser func(message *goredis.Message) (T, error),
	events chan<- SubscribeEvent[T],
) {
	defer close(events)
	defer func() {
		if err := subscription.Close(); err != nil {
			emitFinalSubscribeEvent(events, SubscribeEvent[T]{
				Err: fmt.Errorf("close redis subscription: %w", err),
			})
		}
	}()

	messages := subscription.Channel()
	if messages == nil {
		emitSubscribeEvent(
			ctx,
			events,
			SubscribeEvent[T]{Err: errors.New("redis subscription message stream is nil")},
		)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			if message == nil {
				if !emitSubscribeEvent(
					ctx,
					events,
					SubscribeEvent[T]{
						Err: errors.New("redis subscription received a nil message"),
					},
				) {
					return
				}
				continue
			}
			value, err := parseSubscribeMessage(parser, message)
			event := SubscribeEvent[T]{Message: value, Err: err}
			if !emitSubscribeEvent(ctx, events, event) {
				return
			}
		}
	}
}

// parseSubscribeMessage 把业务解析器 panic 转为事件错误，防止后台 goroutine 终止进程。
func parseSubscribeMessage[T any](
	parser func(message *goredis.Message) (T, error),
	message *goredis.Message,
) (value T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"parse redis subscription message panic: %v\n%s",
				recovered,
				debug.Stack(),
			)
		}
	}()
	return parser(message)
}

// emitSubscribeEvent 在取消时放弃阻塞发送，让订阅 goroutine 能及时退出。
func emitSubscribeEvent[T any](
	ctx context.Context,
	events chan<- SubscribeEvent[T],
	event SubscribeEvent[T],
) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitFinalSubscribeEvent 在消费者仍读取时报告关闭错误，同时用超时避免无人读取时泄漏 goroutine。
func emitFinalSubscribeEvent[T any](
	events chan<- SubscribeEvent[T],
	event SubscribeEvent[T],
) {
	timer := time.NewTimer(subscribeCloseEventTimeout)
	defer timer.Stop()
	select {
	case events <- event:
	case <-timer.C:
	}
}

const defaultSubscribeBufferSize = 100

type subscribeOption struct {
	bufferSize int
}

// SubscribeOption 在接收循环启动前配置订阅。
type SubscribeOption func(*subscribeOption)

// WithSubscribeBufferSize 设置单一事件流容量；零表示无缓冲。
func WithSubscribeBufferSize(size int) SubscribeOption {
	return func(option *subscribeOption) {
		option.bufferSize = size
	}
}
