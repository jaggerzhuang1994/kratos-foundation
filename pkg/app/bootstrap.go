// Package app 提供应用级别的启动引导接口
//
// Bootstrap 接口允许业务代码在应用启动时执行自定义初始化逻辑
// 业务侧应该向 Wire ProviderSet 绑定具体的 Bootstrap 实现
//
// 使用方式：
//
//	// 在业务代码中实现 Bootstrap
//	type MyBootstrap struct{}
//
//	func (b *MyBootstrap) Bootstrap() error {
//	    // 执行初始化逻辑
//	    return nil
//	}
//
//	// 在 wire.go 中绑定
//	var ProviderSet = wire.NewSet(
//	    wire.Bind(new(app.Bootstrap), new(*MyBootstrap)),
//	    ...
//	)
package app

// Bootstrap 应用启动引导接口
//
// 该接口定义了应用启动时的初始化逻辑入口，用于执行：
//   - 数据迁移
//   - 缓存预热
//   - 依赖检查
//   - 自定义初始化逻辑
//
// 注意事项：
//   - 业务侧应该向 ProviderSet Bind 这个接口的具体实现
//   - 如果没有自定义逻辑，可以使用 DefaultBootstrap 默认实现
//   - 禁止在 Bootstrap 中注入 app（防止循环依赖）
type Bootstrap any

// DefaultBootstrap 提供默认的启动引导实现
//
// 返回 nil 表示不需要自定义启动引导逻辑，适合不需要特殊初始化的应用
//
// 使用示例：
//
//	// 在 wire.go 中使用默认实现
//	var ProviderSet = wire.NewSet(
//	    app.DefaultBootstrap, // 使用默认实现
//	    ...
//	)
func DefaultBootstrap() Bootstrap {
	return nil
}
