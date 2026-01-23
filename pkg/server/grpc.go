// Package server 提供 gRPC 服务器的创建和配置功能
//
// 该文件定义了 gRPC 服务器的类型别名和工厂函数，通过依赖注入
// 自动创建和配置 gRPC 服务器实例。
//
// 主要功能：
//   - 创建 gRPC 服务器实例
//   - 配置网络、地址、端点
//   - 应用服务器选项和中间件
//   - 注册到服务器管理器
package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
)

// GrpcServer gRPC 服务器类型别名
//
// 该类型是 Kratos 框架的 gRPC.Server 的别名，简化了类型引用
type GrpcServer = *grpc.Server

// NewGrpcServer 创建并配置 gRPC 服务器
//
// 该函数根据配置创建 gRPC 服务器，并将其注册到服务器管理器：
//  1. 检查配置是否禁用 gRPC 服务器
//  2. 使用服务器选项创建 gRPC 服务器实例
//  3. 将服务器注册到 Register 管理器
//
// 参数说明：
//   - _: Setup 接口（未使用，仅用于确保依赖注入顺序）
//   - config: 服务器配置（网络、地址、端点等）
//   - opts: gRPC 服务器选项（通过 NewGrpcServerOptions 创建）
//   - register: 服务器注册器，用于管理所有服务器实例
//
// 返回：
//   - GrpcServer: 配置好的 gRPC 服务器实例，如果禁用则返回 nil
//
// 注意事项：
//   - 如果 config.Grpc.Disable 为 true，返回 nil
//   - 服务器会通过 Register.RegisterServer 注册到管理器
func NewGrpcServer(
	_ Setup, // Setup 接口（确保在服务器创建之前执行）
	config Config, // 服务器配置
	opts GrpcServerOptions, // gRPC 服务器选项
	register Register, // 服务器注册器
) GrpcServer {
	if config.GetGrpc().GetDisable() {
		return nil
	}
	srv := grpc.NewServer(opts.Get()...)
	register.RegisterServer(srv)
	return srv
}

// GrpcServerOptions gRPC 服务器选项的类型别名
// 用于在依赖注入中传递服务器选项
type GrpcServerOptions = SliceT[grpc.ServerOption]

// grpcServerOptions gRPC 服务器选项的内部实现
type grpcServerOptions = sliceT[grpc.ServerOption]

// NewGrpcServerOptions 创建 gRPC 服务器选项
//
// 该函数根据配置构建 gRPC 服务器的所有选项：
//  1. 网络类型（tcp/tcp4/tcp6/unix）
//  2. 监听地址（host:port）
//  3. 对外暴露的端点（用于服务发现）
//  4. 超时设置（禁用默认超时，由中间件控制）
//  5. 健康检查配置
//  6. 反射服务配置
//  7. 中间件链
//
// 参数说明：
//   - config: 服务器配置
//   - middleware: 中间件链（通过 NewMiddlewares 创建）
//
// 返回：
//   - GrpcServerOptions: 包含所有服务器选项的集合
//
// 配置说明：
//   - Network: 监听的网络类型（"tcp", "tcp4", "tcp6", "unix" 或 "unixpacket"）
//   - Addr: 监听的地址（如 ":9000" 或 "0.0.0.0:9000"）
//   - Endpoint: 对外暴露的端点（如 "grpc://service-name:9000"）
//   - Timeout: 设置为 0，禁用默认超时，由中间件控制超时行为
//   - CustomHealth: 是否使用自定义健康检查
//   - DisableReflection: 是否禁用 gRPC 反射服务
//
// 注意事项：
//   - 超时设置为 0 是为了使用中间件级别的超时控制
//   - 反射服务默认启用，便于调试（可通过配置禁用）
func NewGrpcServerOptions(config Config, middleware Middlewares) GrpcServerOptions {
	conf := config.GetGrpc()
	var opts grpcServerOptions

	// 配置网络类型
	// 支持：tcp（默认）、tcp4（IPv4 only）、tcp6（IPv6 only）、unix、unixpacket
	if conf.GetNetwork() != "" {
		opts = append(opts, grpc.Network(conf.GetNetwork()))
	}
	// 配置监听地址
	// 格式：host:port，如 ":9000" 或 "0.0.0.0:9000"
	if conf.GetAddr() != "" {
		opts = append(opts, grpc.Address(conf.GetAddr()))
	}
	// 配置对外暴露的端点
	// 用于服务发现，告诉其他服务如何访问此 gRPC 服务
	if conf.GetEndpoint() != nil {
		opts = append(opts, grpc.Endpoint(&url.URL{Scheme: conf.GetEndpoint().GetScheme(), Host: conf.GetEndpoint().GetHost()}))
	}
	// 禁用默认超时，由中间件来控制超时行为
	// 否则内部会有默认值 1s，可能导致长时间请求被中断
	opts = append(opts, grpc.Timeout(0))
	// 配置自定义健康检查
	if conf.GetCustomHealth() {
		opts = append(opts, grpc.CustomHealth())
	}
	// 禁用反射服务（生产环境建议禁用）
	if conf.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	// 应用中间件链
	opts = append(opts, grpc.Middleware(middleware.Get()...))
	return &opts
}
