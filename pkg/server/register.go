// Package server 提供服务器注册器的功能
//
// 该文件定义了服务器注册接口和实现，用于管理所有传输层服务器
// （HTTP、gRPC、WebSocket、Job 等）的生命周期。
//
// 主要功能：
//   - 注册和管理多个服务器实例
//   - 支持延迟停止机制（优雅停机）
//   - 为所有服务器提供统一的管理接口
package server

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
)

// Register 服务器注册器接口
//
// 该接口定义了服务器的注册和获取方法，用于管理所有传输层服务器：
//   - RegisterServer: 注册新的服务器实例
//   - GetServers: 获取所有已注册的服务器列表
//
// 使用场景：
//   - 收集所有服务器（HTTP、gRPC、WebSocket、Job）
//   - 统一传递给 Kratos 应用实例
//   - 实现优雅停机和延迟停止
type Register interface {
	GetServers() []transport.Server
	RegisterServer(server transport.Server)
}

// register 服务器注册器的实现
//
// 该结构体维护所有已注册的服务器列表，并根据配置决定
// 是否为服务器添加延迟停止包装器。
type register struct {
	config  Config             // 服务器配置
	servers []transport.Server // 已注册的服务器列表
}

// NewRegister 创建服务器注册器
//
// 该函数创建一个服务器注册器实例，用于管理所有服务器。
//
// 参数说明：
//   - config: 服务器配置（包含 StopDelay 配置）
//
// 返回：
//   - Register: 服务器注册器实例
//
// 注意事项：
//   - 注册器本身不会启动服务器，只负责收集和管理
//   - 实际的启动由 Kratos 应用实例控制
func NewRegister(
	config Config,
) Register {
	r := &register{
		config: config,
	}
	return r
}

// RegisterServer 注册服务器实例
//
// 该方法将服务器添加到注册器中，并根据配置决定是否包装服务器：
//   - 如果服务器实现了 Endpointer 接口
//   - 且配置了 StopDelay > 0
//   - 则包装服务器，实现延迟停止机制
//
// 延迟停止机制的作用：
//   - 在服务注销后，延迟停止服务器
//   - 避免其他服务还在使用旧连接时请求失败
//   - 配合服务发现中心实现平滑下线
//
// 参数说明：
//   - server: 要注册的服务器实例（HTTP、gRPC、WebSocket、Job 等）
//
// 工作流程：
//  1. 检查服务器是否实现了 Endpointer 接口（是否有对外暴露的端点）
//  2. 检查配置的 StopDelay 是否大于 0
//  3. 如果两者都满足，则用 serverStopDelayWrapper 包装服务器
//  4. 否则直接注册原始服务器
//
// 注意事项：
//   - 延迟停止只适用于有对外端点的服务器（HTTP、gRPC）
//   - 内部服务器（如 Job）通常不需要延迟停止
func (s *register) RegisterServer(server transport.Server) {
	// 如果存在对外暴露端点，且 stop_delay > 0，则套一层 serverStopDelayWrapper 来延迟停止服务
	// 避免注册到服务中心停止服务导致其他服务无法请求
	if endpointer, ok := server.(transport.Endpointer); ok && s.config.GetStopDelay().AsDuration() > 0 {
		s.servers = append(s.servers, &serverStopDelayWrapper{
			Server:     server,
			Endpointer: endpointer,
			stopDelay:  s.config.GetStopDelay().AsDuration(),
		})
	} else {
		s.servers = append(s.servers, server)
	}
}

// GetServers 获取所有已注册的服务器
//
// 该方法返回所有已注册的服务器列表，用于传递给 Kratos 应用实例。
//
// 返回：
//   - []transport.Server: 所有已注册的服务器列表
//
// 注意事项：
//   - 返回的列表可能包含被 serverStopDelayWrapper 包装的服务器
//   - 服务器顺序即为注册顺序
func (s *register) GetServers() []transport.Server {
	return s.servers
}

// serverStopDelayWrapper 服务器延迟停止包装器
//
// 该结构体包装了原始服务器，实现了延迟停止机制：
//   - 在 Stop 方法被调用时，先等待 stopDelay 时间
//   - 然后再调用原始服务器的 Stop 方法
//
// 使用场景：
//   - 配合服务发现中心实现优雅下线
//   - 在服务注销后，继续服务一段时间
//   - 让其他服务有时间更新服务列表
//
// 字段说明：
//   - Server: 原始服务器实例
//   - Endpointer: 原始服务器的端点信息
//   - stopDelay: 延迟停止的时间长度
type serverStopDelayWrapper struct {
	transport.Server
	transport.Endpointer
	stopDelay time.Duration
}

// Stop 延迟停止服务器
//
// 该方法实现了延迟停止逻辑：
//  1. 等待 stopDelay 时间
//  2. 调用原始服务器的 Stop 方法
//
// 参数说明：
//   - ctx: 停止上下文（未使用，延迟时间是固定的）
//
// 返回：
//   - error: 原始服务器 Stop 方法的返回值
//
// 注意事项：
//   - 该方法会阻塞 stopDelay 时间
//   - 在延迟期间，服务器继续接受和处理请求
//   - 延迟时间应该大于服务发现的刷新间隔
func (s *serverStopDelayWrapper) Stop(ctx context.Context) error {
	if s.stopDelay > 0 {
		time.Sleep(s.stopDelay)
	}
	return s.Server.Stop(ctx)
}
