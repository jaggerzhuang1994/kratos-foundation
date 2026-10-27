package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Runtime 持有按同一份配置组装出的 HTTP、gRPC 和停机策略。
type Runtime struct {
	// health 业务和管理端口共享的健康状态。
	health *healthState
	// http 业务 HTTP 服务；禁用时为 nil。
	http *httpRuntime
	// grpc 业务 gRPC 服务；禁用时为 nil。
	grpc GRPCServer
	// management 独立管理 HTTP 服务，由应用统一启停。
	management []*httpRuntime
	// httpServer、grpcServer 是附加停机策略后的业务服务；禁用的协议为 nil。
	httpServer, grpcServer transport.Server
	// managementServers 是附加停机策略、声明 RegistrationExempt 的独立管理监听。
	managementServers []transport.Server
	// stopDelay 撤销就绪状态后等待流量退出的时长。
	stopDelay time.Duration
}

// NewRuntime 创建服务器运行时，供业务与 Wire 组装层使用。
func NewRuntime(
	configManager foundationconfig.Manager,
	logger log.Logger,
	metricsProvider metrics.Provider,
	tracingProvider tracing.Provider,
	spec *Spec,
) (*Runtime, func(), error) {
	logger = logger.WithModule("server")
	if err := spec.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate server spec: %w", err)
	}
	config, err := loadConfig(configManager)
	if err != nil {
		return nil, nil, err
	}
	policies, cleanup, err := newMiddlewarePolicies(
		configManager,
		logger,
		config,
		metricsProvider,
		tracingProvider,
	)
	if err != nil {
		return nil, nil, err
	}
	var httpServer *httpRuntime
	var management []*httpRuntime
	closeListeners := func() error {
		var errs []error
		for index := len(management) - 1; index >= 0; index-- {
			if err := management[index].AbortStartup(context.Background()); err != nil {
				errs = append(errs, fmt.Errorf("close management HTTP listener %s://%s: %w", management[index].listener.network, management[index].listener.address, err))
			}
		}
		if httpServer != nil {
			if err := httpServer.AbortStartup(context.Background()); err != nil {
				errs = append(errs, fmt.Errorf("close business HTTP listener %s://%s: %w", httpServer.listener.network, httpServer.listener.address, err))
			}
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (*Runtime, func(), error) {
		err = errors.Join(err, closeListeners())
		cleanup()
		return nil, nil, err
	}
	middlewares := newMiddlewares(policies)
	httpOptions := newHTTPServerOptions(config, middlewares, spec)
	websockets := newWebSocketHub()
	health := configuredHealth(config, spec)
	if err := health.registerReadinessMetric(metricsProvider.Meter("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server")); err != nil {
		return fail(err)
	}
	httpServer, err = newHTTPServer(
		config,
		httpOptions,
		spec,
		logger,
		websockets,
	)
	if err != nil {
		return fail(err)
	}
	grpcOptions := newGRPCServerOptions(config, middlewares, spec, logger)
	grpcServer, err := newGRPCServer(config, grpcOptions, spec, health)
	if err != nil {
		return fail(err)
	}
	var business HTTPServer
	if httpServer != nil {
		business = httpServer.HTTPServer
	}
	var monitoringEndpoints []registeredEndpoint
	management, monitoringEndpoints, err = configureMonitoring(config, business, health, metricsProvider)
	if err != nil {
		return fail(err)
	}
	var httpNames, grpcNames serviceNames
	var routeNames map[httpRoute]string
	var grpcServiceNames map[string]string
	if business != nil {
		httpNames, routeNames, err = collectHTTPServiceNames(business, config.GetHttp().GetPathPrefix(), len(spec.http.registrations) > 0, newHTTPRouteIndex(protoregistry.GlobalFiles))
		if err != nil {
			return fail(err)
		}
	}
	if grpcServer != nil {
		grpcNames, grpcServiceNames = collectGRPCServiceNames(grpcServer, protoregistry.GlobalFiles)
	}
	if err := logRegisteredEndpoints(logger, business, grpcServer, monitoringEndpoints, routeNames, grpcServiceNames); err != nil {
		return fail(err)
	}
	// 记录配置的监听地址而非实际绑定端口；:0 等动态端口以 SDK 启动时的 listening 日志为准。
	httpAddress, grpcAddress := "disabled", "disabled"
	if httpServer != nil {
		httpAddress = httpServer.listener.address
	}
	if grpcServer != nil {
		grpcAddress = cmp.Or(config.GetGrpc().GetAddr(), ":0")
	}
	managementAddresses := make([]string, len(management))
	for index, server := range management {
		managementAddresses[index] = server.listener.address
	}
	logger.With("event", "server.assembled", "http", httpAddress, "grpc", grpcAddress,
		"management", managementAddresses, "stop_delay", config.GetStopDelay().AsDuration(),
		"http_services", httpNames.names, "grpc_services", grpcNames.names).Info("servers assembled")
	stopDelay := config.GetStopDelay().AsDuration()
	runtime := &Runtime{health: health, http: httpServer, grpc: grpcServer, management: management, stopDelay: stopDelay}
	if httpServer != nil {
		runtime.httpServer = withServiceNames(withStopLifecycle(httpServer, stopDelay, websockets.stop, health), httpNames)
	}
	if grpcServer != nil {
		runtime.grpcServer = withServiceNames(withStopLifecycle(grpcServer, stopDelay, nil, nil), grpcNames)
	}
	for _, srv := range management {
		runtime.managementServers = append(runtime.managementServers, newManagementRuntime(withStopLifecycle(srv, stopDelay, nil, health)))
	}
	return runtime, sync.OnceFunc(func() {
		health.stopped.Store(true)
		if err := closeListeners(); err != nil {
			logger.With("event", "server.listener.cleanup.failed", "error", err).Error("listener cleanup failed")
		}
		cleanup()
	}), nil
}

type registeredEndpoint struct {
	transport string
	service   string
	method    string
	path      string
	listener  string
	// serviceName 由 proto 推导的服务名；框架端点和未映射的路由为空。
	serviceName string
}

// logRegisteredEndpoints 在服务启动前输出最终路由表及每个入口归属的服务名，便于核对实际暴露的入口。
func logRegisteredEndpoints(logger log.Logger, httpServer HTTPServer, grpcServer GRPCServer, extra []registeredEndpoint, routeNames map[httpRoute]string, grpcServiceNames map[string]string) error {
	endpoints := append([]registeredEndpoint(nil), extra...)
	if httpServer != nil {
		if err := httpServer.WalkRoute(func(routeInfo http.RouteInfo) error {
			endpoints = append(endpoints, registeredEndpoint{
				transport:   "http",
				method:      routeInfo.Method,
				path:        routeInfo.Path,
				listener:    "business",
				serviceName: routeNames[httpRoute{method: routeInfo.Method, path: routeInfo.Path}],
			})
			return nil
		}); err != nil {
			return fmt.Errorf("walk HTTP endpoints: %w", err)
		}
	}
	if grpcServer != nil {
		for service, info := range grpcServer.GetServiceInfo() {
			for _, method := range info.Methods {
				endpoints = append(endpoints, registeredEndpoint{
					transport:   "grpc",
					service:     service,
					method:      method.Name,
					path:        "/" + service + "/" + method.Name,
					listener:    "business",
					serviceName: grpcServiceNames[service],
				})
			}
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].transport != endpoints[j].transport {
			return endpoints[i].transport == "http"
		}
		if endpoints[i].path != endpoints[j].path {
			return endpoints[i].path < endpoints[j].path
		}
		return endpoints[i].method < endpoints[j].method
	})
	for _, endpoint := range endpoints {
		logger.With(
			"event", "server.endpoint.registered",
			"transport", endpoint.transport,
			"method", endpoint.method,
			"path", endpoint.path,
			"service", endpoint.service,
			"service_name", endpoint.serviceName,
			"listener", endpoint.listener,
		).Debug("endpoint registered")
	}
	return nil
}

// StopDelay 返回构造时已经校验的停机等待时间，供调用方了解服务器停机策略。
func (r *Runtime) StopDelay() time.Duration {
	return r.stopDelay
}

// Servers 返回已经附加停机策略的 HTTP 和 gRPC Server；禁用的协议对应 nil，重复调用返回同一实例。
func (r *Runtime) Servers() (transport.Server, transport.Server) {
	return r.httpServer, r.grpcServer
}

// withStopLifecycle 仅在需要附加停机动作或延迟时包装 Server，并保留 Endpointer 能力。
func withStopLifecycle(
	server transport.Server,
	delay time.Duration,
	alongside func(context.Context) error,
	health *healthState,
) transport.Server {
	if delay <= 0 && alongside == nil && health == nil {
		return server
	}

	runtime := &stopRuntime{
		Server:    server,
		health:    health,
		delay:     delay,
		alongside: alongside,
	}
	if endpointer, ok := server.(transport.Endpointer); ok {
		return &endpointStopRuntime{
			stopRuntime: runtime,
			Endpointer:  endpointer,
		}
	}
	return runtime
}

type stopRuntime struct {
	// health 停机时需要撤销就绪的健康状态，可为 nil。
	health *healthState
	// Server 被包装并委托启停的底层服务。
	transport.Server
	// delay 调用底层停止前的等待时长。
	delay time.Duration
	// alongside 与底层停止并行执行的附加停机动作，可为 nil。
	alongside func(context.Context) error
}

type endpointStopRuntime struct {
	// stopRuntime 附加停机策略的服务包装。
	*stopRuntime
	// Endpointer 保留底层服务的端点查询能力。
	transport.Endpointer
}

// Stop 等待配置延迟后并行执行附加停机动作和底层 Server 停止，并合并所有错误。
func (s *stopRuntime) Stop(ctx context.Context) error {
	if s.health != nil {
		s.health.stopped.Store(true)
	}
	if s.delay > 0 {
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), s.stopTogether(ctx))
		}
	}
	return s.stopTogether(ctx)
}

// stopTogether 让附加动作与底层停止共用同一预算而不是先后消耗：WebSocket 等已劫持的连接
// 不受 http.Server.Shutdown 跟踪，串行时卡住的业务回调会耗尽预算，使普通请求被强制关闭。
func (s *stopRuntime) stopTogether(ctx context.Context) error {
	if s.alongside == nil {
		return s.Server.Stop(ctx)
	}
	alongside := make(chan error, 1)
	go func() {
		// 附加停机动作在独立协程中执行，panic 转为停止错误，不让进程跳过其余停机步骤直接退出。
		defer func() {
			if recovered := recover(); recovered != nil {
				alongside <- fmt.Errorf("server stop action panicked with %T", recovered)
			}
		}()
		alongside <- s.alongside(ctx)
	}()
	err := s.Server.Stop(ctx)
	return errors.Join(<-alongside, err)
}

// AbortStartup 透传启动前回滚，不执行停止延迟或尚未启动的业务停止回调。
func (s *stopRuntime) AbortStartup(ctx context.Context) error {
	if runtime, ok := s.Server.(interface{ AbortStartup(context.Context) error }); ok {
		return runtime.AbortStartup(ctx)
	}
	return nil
}

// ManagementServers 返回独立监控监听的运行时；它们实现 Endpointer 便于诊断，并声明 RegistrationExempt，不会注册成业务发现地址。
// 使用 NewServerBootstrap 时内部自动登记；手工组装须与 Servers 返回值一起登记。重复调用返回同一实例。
func (r *Runtime) ManagementServers() []transport.Server {
	return slices.Clone(r.managementServers)
}

// managementRuntime 包装独立管理监听：公布端点但声明不参与服务注册，管理端口不会进入服务注册的 endpoint 列表。
type managementRuntime struct {
	// Server 被委托启停的管理服务。
	transport.Server
	// Endpointer 公布管理监听地址，只用于诊断。
	transport.Endpointer
}

// newManagementRuntime 包装管理监听；底层服务总是实现 Endpointer。
func newManagementRuntime(server transport.Server) *managementRuntime {
	endpointer, _ := server.(transport.Endpointer)
	return &managementRuntime{Server: server, Endpointer: endpointer}
}

// RegistrationExempt 声明管理端口不参与服务注册。
func (s *managementRuntime) RegistrationExempt() bool { return true }

// AbortStartup 保留管理监听的回滚能力。
func (s *managementRuntime) AbortStartup(ctx context.Context) error {
	return s.Server.(interface{ AbortStartup(context.Context) error }).AbortStartup(ctx)
}
