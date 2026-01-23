// Package server 提供服务器启动引导接口
//
// Bootstrap 接口允许业务代码在服务器初始化完成后执行自定义逻辑
// 此时 HTTP/gRPC/WebSocket 服务器已经创建完成，可以进行：
//   - 注册自定义路由
//   - 配置服务器选项
//   - 初始化服务器级资源
//
// 使用方式：
//
//	// 在业务代码中实现 Bootstrap
//	type MyServerBootstrap struct {
//	    httpServer  server.HttpServer
//	    grpcServer  server.GrpcServer
//	    wsServer    websocket.Server
//	}
//
//	func (b *MyServerBootstrap) Bootstrap() error {
//	    // 服务器初始化完成，可以注册路由等
//	    return nil
//	}
//
//	// 在 wire.go 中绑定
//	var ProviderSet = wire.NewSet(
//	    wire.Bind(new(server.Bootstrap), new(*MyServerBootstrap)),
//	    ...
//	)
package server

// Bootstrap 服务器启动引导接口
//
// 该接口定义了服务器初始化完成后的回调入口，用于：
//   - 注册自定义 HTTP/gRPC 路由
//   - 配置 WebSocket 处理器
//   - 初始化服务器级别的资源
//   - 执行服务器相关的自定义逻辑
//
// 与 Setup 的区别：
//   - Setup: 在服务器创建之前执行，用于提供配置
//   - Bootstrap: 在服务器创建之后执行，用于操作已创建的服务器实例
//
// 注意事项：
//   - 可以在 Bootstrap 中注入 HttpServer、GrpcServer、WebsocketServer
//   - 业务侧应该向 ProviderSet Bind 这个接口的具体实现
//   - 如果没有自定义逻辑，可以使用 NewDefaultBootstrap 默认实现
type Bootstrap any

// NewDefaultBootstrap 提供默认的服务器启动引导实现
//
// 返回 nil 表示不需要自定义服务器初始化逻辑，适合不需要特殊配置的应用
//
// 参数说明：
//   - _: HttpServer 实例（未使用，仅用于确保依赖注入顺序）
//   - _: GrpcServer 实例（未使用，仅用于确保依赖注入顺序）
//
// 使用示例：
//
//	// 在 wire.go 中使用默认实现
//	var ProviderSet = wire.NewSet(
//	    server.NewDefaultBootstrap, // 使用默认实现
//	    ...
//	)
func NewDefaultBootstrap(
	_ HttpServer, // HTTP 服务器实例（确保在 Bootstrap 之前创建）
	_ GrpcServer, // gRPC 服务器实例（确保在 Bootstrap 之前创建）
) Bootstrap {
	return nil
}
