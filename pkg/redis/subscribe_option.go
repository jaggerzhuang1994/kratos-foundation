// Package redis 提供 Redis 订阅的配置选项
package redis

// subscribeOption 订阅选项配置
type subscribeOption struct {
	chSize    int // 消息的 channel 大小
	errChSize int // 错误的 channel 大小
}

// SubscribeOption 订阅选项函数类型
type SubscribeOption func(*subscribeOption)

// WithSubscribeChannelSize 设置消息 channel 的大小
func WithSubscribeChannelSize(chSize int) SubscribeOption {
	return func(option *subscribeOption) {
		option.chSize = chSize
	}
}

// WithSubscribeErrorChannelSize 设置错误 channel 的大小
func WithSubscribeErrorChannelSize(errChSize int) SubscribeOption {
	return func(option *subscribeOption) {
		option.errChSize = errChSize
	}
}
