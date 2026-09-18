package server

import (
	"fmt"
	"strings"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

// HTTPEndpoint 在 HTTPServer 上注册一组应用路由。
type HTTPEndpoint func(HTTPServer) error

// GRPCService 在 GRPCServer 上注册一个应用服务。
type GRPCService func(GRPCServer) error

// HTTPBuilder 在启动配置阶段记录业务 HTTP 运行选项；所有方法修改同一个 Spec，不能并发调用。
type HTTPBuilder interface {
	// Middleware 以默认业务优先级追加中间件。
	Middleware(...middleware.Middleware) HTTPBuilder
	// MiddlewareSpec 追加带名称或显式优先级的中间件。
	MiddlewareSpec(...MiddlewareSpec) HTTPBuilder
	// Option 追加原生 Kratos HTTP ServerOption。
	Option(...http.ServerOption) HTTPBuilder
	// Register 追加应用路由注册回调。
	Register(...HTTPEndpoint) HTTPBuilder
	// WebSocket 注册一条 WebSocket 路径及事件处理器，默认消息上限 1 MiB。
	WebSocket(path string, handler any, optionalUpgrader ...Upgrader) HTTPBuilder
	// WebSocketWithConfig 注册带消息上限和握手设置的 WebSocket 端点。
	WebSocketWithConfig(path string, handler any, config WebSocketConfig) HTTPBuilder
}

// GRPCBuilder 在启动配置阶段记录 gRPC 运行选项；所有方法修改同一个 Spec，不能并发调用。
type GRPCBuilder interface {
	// Middleware 以默认业务优先级追加中间件。
	Middleware(...middleware.Middleware) GRPCBuilder
	// MiddlewareSpec 追加带名称或显式优先级的中间件。
	MiddlewareSpec(...MiddlewareSpec) GRPCBuilder
	// Option 追加原生 Kratos gRPC ServerOption。
	Option(...grpc.ServerOption) GRPCBuilder
	// Register 追加应用服务注册回调。
	Register(...GRPCService) GRPCBuilder
}

// Spec 保存业务 HTTP、gRPC 和独立健康检查声明，仅在组装前串行修改。
type Spec struct {
	// http 构造期收集的 HTTP 注册信息。
	http httpSpec
	// grpc 构造期收集的 gRPC 注册信息。
	grpc grpcSpec
	// health 构造期收集的就绪检查。
	health HealthBuilder
}

type httpSpec struct {
	// middlewares 业务声明的 HTTP 中间件及优先级。
	middlewares []MiddlewareSpec
	// options 传给 HTTP 服务构造函数的选项。
	options []http.ServerOption
	// endpoints HTTP 路由注册回调。
	endpoints []HTTPEndpoint
	// websockets WebSocket 路由与处理配置。
	websockets []websocketEndpoint
}

type grpcSpec struct {
	// middlewares 业务声明的 gRPC 中间件及优先级。
	middlewares []MiddlewareSpec
	// options 传给 gRPC 服务构造函数的选项。
	options []grpc.ServerOption
	// services gRPC 服务注册回调。
	services []GRPCService
}

// WebSocketConfig 控制单个端点的握手、接收限制与消息调度，构造时读取，不热更新。
type WebSocketConfig struct {
	// Upgrader 配置协议握手，零值使用 Gorilla 默认行为。
	Upgrader Upgrader
	// MaxMessageBytes 同时限制接收消息的传输载荷与解压后字节；0 使用默认 1 MiB，-1 显式取消上限。
	MaxMessageBytes int64
	// MaxInFlightMessages 限制单连接并发处理的消息数；0 使用默认值 1，负数无效。
	MaxInFlightMessages int
}

type websocketEndpoint struct {
	// maxMessageBytes 单条消息字节上限；0 使用 1 MiB，-1 不限制，覆盖传输及解压后载荷。
	maxMessageBytes int64
	// maxInFlightMessages 单连接同时执行的 OnMessage 数量；0 使用默认值 1。
	maxInFlightMessages int
	// path WebSocket 握手路由路径，须以 / 开头且不可重复。
	path string
	// handler 非 nil 业务对象，须至少实现一种受支持的 WebSocket 回调接口。
	handler any
	// upgrader 可选握手升级器，最多一个；省略时采用默认升级器。
	upgrader []Upgrader
}

// NewSpec 返回一个空服务定义；零值 Spec 也可以安全使用。
func NewSpec() *Spec {
	return &Spec{}
}

// HTTP 返回业务 HTTP 构建器；默认开启，配置 disable=true 可关闭业务监听。
func (s *Spec) HTTP() HTTPBuilder {
	return &s.http
}

// GRPC 返回业务 gRPC 构建器；有效服务注册默认开启，显式配置优先。
func (s *Spec) GRPC() GRPCBuilder {
	return &s.grpc
}

// Validate 拒绝无效或重复的 WebSocket 端点。
func (s *Spec) Validate() error {
	paths := make(map[string]struct{}, len(s.http.websockets))
	for _, endpoint := range s.http.websockets {
		if !strings.HasPrefix(endpoint.path, "/") {
			return fmt.Errorf("websocket path %q must start with /", endpoint.path)
		}
		if endpoint.handler == nil {
			return fmt.Errorf("websocket handler for %q is nil", endpoint.path)
		}
		if !isWebSocketHandler(endpoint.handler) {
			return fmt.Errorf(
				"websocket handler for %q implements no supported handler interface",
				endpoint.path,
			)
		}
		if endpoint.maxMessageBytes < -1 {
			return fmt.Errorf("websocket path %q max message bytes must be -1 or non-negative", endpoint.path)
		}
		if endpoint.maxInFlightMessages < 0 {
			return fmt.Errorf("websocket path %q max in-flight messages must be non-negative", endpoint.path)
		}
		if len(endpoint.upgrader) > 1 {
			return fmt.Errorf(
				"websocket path %q has %d upgraders; want at most one",
				endpoint.path,
				len(endpoint.upgrader),
			)
		}
		if _, ok := paths[endpoint.path]; ok {
			return fmt.Errorf("websocket path %q is already registered", endpoint.path)
		}
		paths[endpoint.path] = struct{}{}
	}
	return nil
}

// isWebSocketHandler 判断对象是否实现至少一种受支持的 WebSocket 事件接口。
func isWebSocketHandler(handler any) bool {
	if _, ok := handler.(OnHandshakeHandler); ok {
		return true
	}
	if _, ok := handler.(OnConnectHandler); ok {
		return true
	}
	if _, ok := handler.(OnErrorHandler); ok {
		return true
	}
	if _, ok := handler.(OnMessageHandler); ok {
		return true
	}
	_, ok := handler.(OnCloseHandler)
	return ok
}

// Middleware 追加非 nil 的默认优先级业务中间件。
func (s *httpSpec) Middleware(middlewares ...middleware.Middleware) HTTPBuilder {
	for _, middleware := range middlewares {
		if middleware != nil {
			s.middlewares = append(s.middlewares, MiddlewareSpec{
				Priority:   MiddlewarePriorityCustom,
				Middleware: middleware,
			})
		}
	}
	return s
}

// MiddlewareSpec 追加具名或自定义优先级的 HTTP 中间件描述。
func (s *httpSpec) MiddlewareSpec(specs ...MiddlewareSpec) HTTPBuilder {
	s.middlewares = append(s.middlewares, specs...)
	return s
}

// Option 追加非 nil 的原生 HTTP 服务选项。
func (s *httpSpec) Option(options ...http.ServerOption) HTTPBuilder {
	for _, option := range options {
		if option != nil {
			s.options = append(s.options, option)
		}
	}
	return s
}

// Register 追加非 nil 的 HTTP 路由注册回调。
func (s *httpSpec) Register(endpoints ...HTTPEndpoint) HTTPBuilder {
	for _, endpoint := range endpoints {
		if endpoint != nil {
			s.endpoints = append(s.endpoints, endpoint)
		}
	}
	return s
}

// WebSocket 保存端点定义的独立副本，默认消息上限 1 MiB。
// upgrader 切片独立复制；自定义上限使用 WebSocketWithConfig。
func (s *httpSpec) WebSocket(path string, handler any, optionalUpgrader ...Upgrader) HTTPBuilder {
	s.websockets = append(s.websockets, websocketEndpoint{
		path:     path,
		handler:  handler,
		upgrader: append([]Upgrader(nil), optionalUpgrader...),
	})
	return s
}

// WebSocketWithConfig 复用端点登记校验，并保存本端点的接收上限。
func (s *httpSpec) WebSocketWithConfig(path string, handler any, config WebSocketConfig) HTTPBuilder {
	s.WebSocket(path, handler, config.Upgrader)
	s.websockets[len(s.websockets)-1].maxMessageBytes = config.MaxMessageBytes
	s.websockets[len(s.websockets)-1].maxInFlightMessages = config.MaxInFlightMessages
	return s
}

// Middleware 追加非 nil 的默认优先级业务中间件。
func (s *grpcSpec) Middleware(middlewares ...middleware.Middleware) GRPCBuilder {
	for _, middleware := range middlewares {
		if middleware != nil {
			s.middlewares = append(s.middlewares, MiddlewareSpec{
				Priority:   MiddlewarePriorityCustom,
				Middleware: middleware,
			})
		}
	}
	return s
}

// MiddlewareSpec 追加具名或自定义优先级的 gRPC 中间件描述。
func (s *grpcSpec) MiddlewareSpec(specs ...MiddlewareSpec) GRPCBuilder {
	s.middlewares = append(s.middlewares, specs...)
	return s
}

// Option 追加非 nil 的原生 gRPC 服务选项。
func (s *grpcSpec) Option(options ...grpc.ServerOption) GRPCBuilder {
	for _, option := range options {
		if option != nil {
			s.options = append(s.options, option)
		}
	}
	return s
}

// Register 追加非 nil 的 gRPC 服务注册回调。
func (s *grpcSpec) Register(services ...GRPCService) GRPCBuilder {
	for _, service := range services {
		if service != nil {
			s.services = append(s.services, service)
		}
	}
	return s
}

// Health 返回独立的健康检查声明；监听地址、路径与开关由配置决定。
func (s *Spec) Health() *HealthBuilder { return &s.health }

// HealthBuilder 在组装阶段串行声明关键依赖检查，不配置业务 HTTP。
type HealthBuilder struct {
	// checks 按注册顺序执行的关键依赖就绪检查。
	checks []ReadinessCheck
}

// Checks 追加关键依赖检查并复制切片；检查函数必须支持 Context 且可被探针并发调用。
func (s *HealthBuilder) Checks(checks ...ReadinessCheck) *HealthBuilder {
	s.checks = append(s.checks, checks...)
	return s
}
