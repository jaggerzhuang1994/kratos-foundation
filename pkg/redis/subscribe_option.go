// Package redis 提供 Redis 订阅的配置选项
package redis

// subscribeOption 订阅选项的内部配置结构
// 该结构用于配置 Redis 订阅时的通道缓冲大小
type subscribeOption struct {
	chSize    int // 消息通道的缓冲大小
	errChSize int // 错误通道的缓冲大小
}

// SubscribeOption 订阅选项函数类型
// 采用函数式选项模式（Functional Options Pattern），方便链式调用和扩展
type SubscribeOption func(*subscribeOption)

// WithSubscribeChannelSize 设置消息通道的缓冲大小
//
// 参数：
//   - chSize: 消息通道的缓冲大小（必须大于 0）
//
// 使用建议：
//   - 消息频率高：建议使用较大的缓冲（如 500-1000），避免消息处理不及时导致阻塞
//   - 消息频率低：可以使用默认值 100 或更小
//   - 内存受限环境：应使用较小的缓冲，避免占用过多内存
//
// 注意事项：
//   - 缓冲过小：当消息生产速度大于消费速度时，可能导致发送阻塞
//   - 缓冲过大：会占用更多内存，在高并发场景下需要权衡
//
// 示例：
//
//	msgCh, errCh := redis.Subscribe(rdb, ctx, "channel", parser,
//	    redis.WithSubscribeChannelSize(500),
//	)
func WithSubscribeChannelSize(chSize int) SubscribeOption {
	return func(option *subscribeOption) {
		option.chSize = chSize
	}
}

// WithSubscribeErrorChannelSize 设置错误通道的缓冲大小
//
// 参数：
//   - errChSize: 错误通道的缓冲大小（必须大于 0）
//
// 使用建议：
//   - 通常错误频率较低，默认值 10 足够应对大部分场景
//   - 如果 parser 函数可能频繁出错，建议增大缓冲
//   - 错误处理应该及时，避免错误堆积
//
// 注意事项：
//   - 错误通道满时，新错误无法发送，可能导致错误被忽略
//   - 建议在应用中记录错误日志，便于排查问题
//
// 示例：
//
//	msgCh, errCh := redis.Subscribe(rdb, ctx, "channel", parser,
//	    redis.WithSubscribeErrorChannelSize(50),
//	)
func WithSubscribeErrorChannelSize(errChSize int) SubscribeOption {
	return func(option *subscribeOption) {
		option.errChSize = errChSize
	}
}
