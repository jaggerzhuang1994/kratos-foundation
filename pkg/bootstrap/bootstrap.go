// Package bootstrap 提供应用启动引导的接口定义
package bootstrap

// Bootstrap 应用启动引导接口
// 业务侧应该自己向 ProviderSet Bind 这个接口的具体实现
// 如果没有，则需要提供 DefaultBootstrap 默认实现
type Bootstrap any

// DefaultBootstrap 提供默认的启动引导实现
// 返回 nil 表示不需要自定义启动引导逻辑
func DefaultBootstrap() Bootstrap {
	return nil
}
