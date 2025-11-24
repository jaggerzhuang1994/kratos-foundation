package bootstrap

// Bootstrap 业务侧应该自己向 wire Bind 这个接口的具体实现
// 主要用于一些依赖 Hook 组件的实例去提前初始化 hook
type Bootstrap any

func DefaultBootstrap() Bootstrap {
	return nil
}
