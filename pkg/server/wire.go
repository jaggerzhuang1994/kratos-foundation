// Package server 提供 Wire 依赖注入的 ProviderSet
//
// 该文件定义了服务器模块的所有依赖注入提供者，通过 Wire 工具
// 自动生成依赖关系的初始化代码。
//
// ProviderSet 包含：
//   - 配置相关：NewDefaultConfig, NewConfig
//   - 中间件：NewMiddlewares
//   - HTTP 服务器：NewHttpServerOptions, NewHttpServer
//   - gRPC 服务器：NewGrpcServerOptions, NewGrpcServer
//   - WebSocket 服务器：NewWebsocketServer
//   - 服务器管理：NewRegister
//
// 使用方式：
//
//	// 在 wire.go 中导入
//	var ProviderSet = wire.NewSet(
//	    server.ProviderSet,
//	    ...
//	)
package server

import (
	"github.com/google/wire"
)

// ProviderSet 服务器模块的依赖注入提供者集合
//
// 该集合按照依赖顺序组织，确保依赖项在被使用者之前初始化：
//  1. 配置和中间件（基础依赖）
//  2. 服务器选项（HTTP/gRPC）
//  3. 服务器实例（HTTP/gRPC/WebSocket）
//  4. 服务器注册器（顶层组件）
//
// 依赖顺序说明：
//   - NewDefaultConfig 和 NewConfig 提供配置
//   - NewMiddlewares 使用配置创建中间件链
//   - NewHttpServerOptions 和 NewGrpcServerOptions 使用配置和中间件
//   - NewHttpServer、NewGrpcServer、NewWebsocketServer 使用选项
//   - NewRegister 收集所有服务器实例
var ProviderSet = wire.NewSet(
	// 配置
	NewDefaultConfig, // 默认配置
	NewConfig,        // 服务器配置（从配置文件加载）

	// 中间件
	NewMiddlewares, // 服务器中间件链

	// HTTP 服务器
	NewHttpServerOptions, // HTTP 服务器选项
	NewHttpServer,        // HTTP 服务器实例

	// gRPC 服务器
	NewGrpcServerOptions, // gRPC 服务器选项
	NewGrpcServer,        // gRPC 服务器实例

	// WebSocket 服务器
	NewWebsocketServer, // WebSocket 服务器实例

	// 服务器注册
	NewRegister, // 服务器注册器（收集所有服务器）
)
