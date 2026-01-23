// Package server 提供服务器配置的注入接口
//
// Setup 接口允许业务代码在服务器创建之前提供自定义配置。
// 此时服务器尚未创建，可以动态地提供配置选项。
//
// 与 Bootstrap 的区别：
//   - Setup: 在服务器创建之前执行，用于提供配置和选项
//   - Bootstrap: 在服务器创建之后执行，用于操作已创建的服务器实例
//
// 使用场景：
//   - 动态配置服务器选项（如 TLS 证书、最大连接数等）
//   - 根据环境变量或其他条件调整服务器配置
package server

// Setup 服务器配置注入接口
//
// 该接口定义了服务器创建前的配置入口，用于：
//   - 提供动态的服务器配置
//   - 根据环境或其他条件调整配置
//   - 避免在服务器创建函数中硬编码配置
//
// 与 Bootstrap 的区别：
//   - Setup: 在服务器创建之前执行，用于提供配置
//     不能注入 HttpServer、GrpcServer、WebsocketServer（防止循环依赖）
//   - Bootstrap: 在服务器创建之后执行，用于操作已创建的服务器实例
//     可以注入 HttpServer、GrpcServer、WebsocketServer
//
// 注意事项：
//   - 业务侧应该向 ProviderSet Bind 这个接口的具体实现
//   - Setup 不能注入 HttpServer、GrpcServer、WebsocketServer 的实例
//   - 如果没有自定义配置，可以使用 NewDefaultSetup 默认实现
type Setup any

// NewDefaultSetup 提供默认的服务器配置实现
//
// 返回 nil 表示不需要自定义服务器配置，适合使用默认配置的应用
//
// 使用示例：
//
//	// 在 wire.go 中使用默认实现
//	var ProviderSet = wire.NewSet(
//	    server.NewDefaultSetup, // 使用默认实现
//	    ...
//	)
func NewDefaultSetup() Setup {
	return nil
}
