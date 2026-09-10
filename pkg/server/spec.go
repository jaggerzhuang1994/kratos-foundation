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

// HTTPBuilder 在启动配置阶段记录 HTTP 运行选项；所有方法修改同一个 Spec，不能并发调用。
type HTTPBuilder interface {
	// Enable 强制启用 HTTP，覆盖配置文件中的 disable。
	Enable() HTTPBuilder
	// Disable 强制关闭 HTTP，覆盖配置文件中的 disable。
	Disable() HTTPBuilder
	// Middleware 以默认业务优先级追加中间件。
	Middleware(...middleware.Middleware) HTTPBuilder
	// MiddlewareSpec 追加带名称或显式优先级的中间件。
	MiddlewareSpec(...MiddlewareSpec) HTTPBuilder
	// Option 追加原生 Kratos HTTP ServerOption。
	Option(...http.ServerOption) HTTPBuilder
	// Register 追加应用路由注册回调。
	Register(...HTTPEndpoint) HTTPBuilder
	// Health 配置默认健康端点及关键依赖检查。
	Health(HealthConfig) HTTPBuilder
	// HealthChecks 追加关键依赖检查，不覆盖配置文件中的监听地址、路径或开关。
	HealthChecks(...ReadinessCheck) HTTPBuilder
	// WebSocket 注册一条 WebSocket 路径及其事件处理器。
	WebSocket(path string, handler any, optionalUpgrader ...Upgrader) HTTPBuilder
}

// GRPCBuilder 在启动配置阶段记录 gRPC 运行选项；所有方法修改同一个 Spec，不能并发调用。
type GRPCBuilder interface {
	// Enable 强制启用 gRPC，覆盖配置文件中的 disable。
	Enable() GRPCBuilder
	// Disable 强制关闭 gRPC，覆盖配置文件中的 disable。
	Disable() GRPCBuilder
	// Middleware 以默认业务优先级追加中间件。
	Middleware(...middleware.Middleware) GRPCBuilder
	// MiddlewareSpec 追加带名称或显式优先级的中间件。
	MiddlewareSpec(...MiddlewareSpec) GRPCBuilder
	// Option 追加原生 Kratos gRPC ServerOption。
	Option(...grpc.ServerOption) GRPCBuilder
	// Register 追加应用服务注册回调。
	Register(...GRPCService) GRPCBuilder
}

// Spec 是 HTTP 与 gRPC 构建器共享的服务定义。
type Spec struct {
	http httpSpec
	grpc grpcSpec
}

type httpSpec struct {
	health       *HealthConfig
	healthChecks []ReadinessCheck
	enabled      *bool
	middlewares  []MiddlewareSpec
	options      []http.ServerOption
	endpoints    []HTTPEndpoint
	websockets   []websocketEndpoint
}

type grpcSpec struct {
	enabled     *bool
	middlewares []MiddlewareSpec
	options     []grpc.ServerOption
	services    []GRPCService
}

type websocketEndpoint struct {
	path     string
	handler  any
	upgrader []Upgrader
}

// NewSpec 返回一个空服务定义；零值 Spec 也可以安全使用。
func NewSpec() *Spec {
	return &Spec{}
}

// HTTP 返回 Spec 中的 HTTP 构建器。
func (s *Spec) HTTP() HTTPBuilder {
	return &s.http
}

// GRPC 返回 Spec 中的 gRPC 构建器。
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

// Enable 强制启用 HTTP。
func (s *httpSpec) Enable() HTTPBuilder {
	enabled := true
	s.enabled = &enabled
	return s
}

// Disable 强制关闭 HTTP。
func (s *httpSpec) Disable() HTTPBuilder {
	enabled := false
	s.enabled = &enabled
	return s
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

// WebSocket 保存端点定义的独立副本，避免调用方随后改写 upgrader 切片。
func (s *httpSpec) WebSocket(path string, handler any, optionalUpgrader ...Upgrader) HTTPBuilder {
	s.websockets = append(s.websockets, websocketEndpoint{
		path:     path,
		handler:  handler,
		upgrader: append([]Upgrader(nil), optionalUpgrader...),
	})
	return s
}

// Enable 强制启用 gRPC。
func (s *grpcSpec) Enable() GRPCBuilder {
	enabled := true
	s.enabled = &enabled
	return s
}

// Disable 强制关闭 gRPC。
func (s *grpcSpec) Disable() GRPCBuilder {
	enabled := false
	s.enabled = &enabled
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

// httpDisabled 合并 Spec 的显式覆盖与配置文件开关。
func (s *Spec) httpDisabled(configDisabled bool) bool {
	if s.http.enabled == nil {
		return configDisabled
	}
	return !*s.http.enabled
}

// grpcDisabled 合并 Spec 的显式覆盖与配置文件开关。
func (s *Spec) grpcDisabled(configDisabled bool) bool {
	if s.grpc.enabled == nil {
		return configDisabled
	}
	return !*s.grpc.enabled
}

// Health 保存健康检查配置副本；仅可在组装阶段修改。
func (s *httpSpec) Health(config HealthConfig) HTTPBuilder {
	config.Checks = append([]ReadinessCheck(nil), config.Checks...)
	s.health = &config
	return s
}

// HealthChecks 只保存代码检查函数，保留 server.http.health 的部署配置。
func (s *httpSpec) HealthChecks(checks ...ReadinessCheck) HTTPBuilder {
	s.healthChecks = append(s.healthChecks, checks...)
	return s
}
