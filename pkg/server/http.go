// Package server 提供 HTTP 服务器的创建和配置功能
//
// 该文件定义了 HTTP 服务器的类型别名和工厂函数，通过依赖注入
// 自动创建和配置 HTTP 服务器实例。
//
// 主要功能：
//   - 创建 HTTP 服务器实例
//   - 配置网络、地址、端点
//   - 注册 Prometheus 指标端点
//   - 应用服务器选项和中间件
//   - 注册到服务器管理器
package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/transport"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HttpServer HTTP 服务器类型别名
//
// 该类型是 Kratos 框架的 http.Server 的别名，简化了类型引用
type HttpServer = *http.Server

// NewHttpServer 创建并配置 HTTP 服务器
//
// 该函数根据配置创建 HTTP 服务器，并将其注册到服务器管理器：
//  1. 检查配置是否禁用 HTTP 服务器
//  2. 使用服务器选项创建 HTTP 服务器实例
//  3. 注册 Prometheus 指标端点（如果启用）
//  4. 将服务器注册到 Register 管理器
//
// 参数说明：
//   - _: Setup 接口（未使用，仅用于确保依赖注入顺序）
//   - config: 服务器配置（网络、地址、端点、指标等）
//   - opts: HTTP 服务器选项（通过 NewHttpServerOptions 创建）
//   - register: 服务器注册器，用于管理所有服务器实例
//
// 返回：
//   - HttpServer: 配置好的 HTTP 服务器实例，如果禁用则返回 nil
//
// 注意事项：
//   - 如果 config.Http.Disable 为 true，返回 nil
//   - Prometheus 指标端点默认路径为 /metrics
//   - 服务器会通过 Register.RegisterServer 注册到管理器
func NewHttpServer(
	_ Setup, // Setup 接口（确保在服务器创建之前执行）
	config Config, // 服务器配置
	opts HttpServerOptions, // HTTP 服务器选项
	register Register, // 服务器注册器
) HttpServer {
	if config.GetHttp().GetDisable() {
		return nil
	}
	srv := http.NewServer(opts.Get()...)
	// 注册 Prometheus 指标端点
	if !config.GetHttp().GetMetrics().GetDisable() {
		srv.Handle(config.GetHttp().GetMetrics().GetPath(), promhttp.Handler())
	}
	register.RegisterServer(srv)
	return srv
}

// HttpServerOptions HTTP 服务器选项的类型别名
// 用于在依赖注入中传递服务器选项
type HttpServerOptions = SliceT[http.ServerOption]

// httpServerOptions HTTP 服务器选项的内部实现
type httpServerOptions = sliceT[http.ServerOption]

// NewHttpServerOptions 创建 HTTP 服务器选项
//
// 该函数根据配置构建 HTTP 服务器的所有选项：
//  1. 网络类型（tcp/tcp4/tcp6/unix）
//  2. 监听地址（host:port）
//  3. 对外暴露的端点（用于服务发现）
//  4. 超时设置（禁用默认超时，由中间件控制）
//  5. 路径前缀
//  6. 严格斜杠匹配
//  7. 错误编码器
//  8. 中间件链
//
// 参数说明：
//   - config: 服务器配置
//   - middleware: 中间件链（通过 NewMiddlewares 创建）
//
// 返回：
//   - HttpServerOptions: 包含所有服务器选项的集合
//
// 配置说明：
//   - Network: 监听的网络类型（"tcp", "tcp4", "tcp6", "unix" 或 "unixpacket"）
//   - Addr: 监听的地址（如 ":8000" 或 "0.0.0.0:8000"）
//   - Endpoint: 对外暴露的端点（如 "http://service-name:8000"）
//   - Timeout: 设置为 0，禁用默认超时，由中间件控制超时行为
//   - PathPrefix: 路由前缀，所有路由都会添加此前缀
//   - DisableStrictSlash: 禁用严格斜杠匹配（/path 和 /path/ 视为不同）
//   - MetricsPath: Prometheus 指标端点路径
//
// 注意事项：
//   - 超时设置为 0 是为了使用中间件级别的超时控制
//   - 错误编码器统一处理 HTTP 错误响应格式
func NewHttpServerOptions(config Config, middleware Middlewares) HttpServerOptions {
	conf := config.GetHttp()
	var opts httpServerOptions
	// 配置网络类型
	// 支持：tcp（默认）、tcp4（IPv4 only）、tcp6（IPv6 only）、unix、unixpacket
	if conf.GetNetwork() != "" {
		opts = append(opts, http.Network(conf.GetNetwork()))
	}
	// 配置监听地址
	// 格式：host:port，如 ":8000" 或 "0.0.0.0:8000"
	if conf.GetAddr() != "" {
		opts = append(opts, http.Address(conf.GetAddr()))
	}
	// 配置对外暴露的端点
	// 用于服务发现，告诉其他服务如何访问此 HTTP 服务
	if conf.GetEndpoint() != nil {
		opts = append(opts, http.Endpoint(&url.URL{Scheme: conf.GetEndpoint().GetScheme(), Host: conf.GetEndpoint().GetHost()}))
	}
	// 禁用默认超时，由中间件来控制超时行为
	// 否则内部会有默认值 1s，可能导致长时间请求被中断
	opts = append(opts, http.Timeout(0))

	// 配置严格斜杠匹配
	// 默认情况下，/path 和 /path/ 视为不同的路径
	// 禁用后，两者被视为相同路径
	if conf.GetDisableStrictSlash() {
		opts = append(opts, http.StrictSlash(false))
	}
	// 配置路径前缀
	// 所有注册的路由都会添加此前缀，如 /api/v1
	if conf.GetPathPrefix() != "" {
		opts = append(opts, http.PathPrefix(conf.GetPathPrefix()))
	}
	// 配置 HTTP 错误编码器
	// 统一处理 HTTP 错误响应格式，包括错误码、消息、详情等
	opts = append(opts, http.ErrorEncoder(transport.HttpErrorEncoder()))
	// 应用中间件链
	opts = append(opts, http.Middleware(middleware.Get()...))
	return &opts
}
