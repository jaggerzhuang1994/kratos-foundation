package bootstrap

// Bootstrap 业务侧应该自己向 wire Bind 这个接口的具体实现
// 如果没有，则需要提供 DefaultBootstrap 默认实现
type Bootstrap any

func DefaultBootstrap() Bootstrap {
	return nil
}

var _ = DefaultBootstrap
