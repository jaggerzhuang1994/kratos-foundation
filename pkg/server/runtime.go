package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// Runtime 持有按同一份配置组装出的 HTTP、gRPC 和停机策略。
type Runtime struct {
	// health 业务和管理端口共享的健康状态。
	health *healthState
	// management 独立管理 HTTP 服务，由应用统一启停。
	management []HTTPServer
	// http 业务 HTTP 服务；禁用时为 nil。
	http HTTPServer
	// grpc 业务 gRPC 服务；禁用时为 nil。
	grpc GRPCServer
	// websockets 跟踪 WebSocket 连接并协调停机的连接集合。
	websockets *websocketHub
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
	fail := func(err error) (*Runtime, func(), error) {
		cleanup()
		return nil, nil, err
	}
	middlewares := newMiddlewares(policies)
	httpOptions := newHTTPServerOptions(config, middlewares, spec)
	websockets := newWebSocketHub()
	health := configuredHealth(config, spec)
	httpServer, err := newHTTPServer(
		config,
		httpOptions,
		spec,
		logger,
		websockets,
	)
	if err != nil {
		return fail(err)
	}
	grpcOptions := newGRPCServerOptions(config, middlewares, spec)
	grpcServer, err := newGRPCServer(config, grpcOptions, spec)
	if err != nil {
		return fail(err)
	}
	management, monitoringEndpoints, err := configureMonitoring(config, httpServer, health, metricsProvider)
	if err != nil {
		return fail(err)
	}
	if err := logRegisteredEndpoints(logger, httpServer, grpcServer, monitoringEndpoints); err != nil {
		return fail(err)
	}
	logger.With("event", "server.assembled",
		"http_enabled", httpServer != nil, "grpc_enabled", grpcServer != nil,
		"management_listeners", len(management)).Info("server.assembled")
	return &Runtime{
		health:     health,
		management: management,
		http:       httpServer,
		grpc:       grpcServer,
		websockets: websockets,
		stopDelay:  config.GetStopDelay().AsDuration(),
	}, func() { health.stopped.Store(true); cleanup() }, nil
}

type registeredEndpoint struct {
	transport string
	service   string
	method    string
	path      string
	listener  string
}

// logRegisteredEndpoints 在服务启动前输出最终路由表，便于核对实际暴露的入口。
func logRegisteredEndpoints(logger log.Logger, httpServer HTTPServer, grpcServer GRPCServer, extra []registeredEndpoint) error {
	endpoints := append([]registeredEndpoint(nil), extra...)
	if httpServer != nil {
		if err := httpServer.WalkRoute(func(routeInfo http.RouteInfo) error {
			endpoints = append(endpoints, registeredEndpoint{
				transport: "http",
				method:    routeInfo.Method,
				path:      routeInfo.Path,
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
					transport: "grpc",
					service:   service,
					method:    method.Name,
					path:      "/" + service + "/" + method.Name,
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
			"event", "endpoint.registered",
			"transport", endpoint.transport,
			"method", endpoint.method,
			"path", endpoint.path,
			"service", endpoint.service,
			"listener", endpoint.listener,
		).Info("endpoint.registered")
	}
	return nil
}

// StopDelay 返回构造时已经校验的停机等待时间，供调用方了解服务器停机策略。
func (r *Runtime) StopDelay() time.Duration {
	return r.stopDelay
}

// Servers 返回已经附加停机策略的 HTTP 和 gRPC Server；禁用的协议对应 nil。
func (r *Runtime) Servers() (transport.Server, transport.Server) {
	var stopWebSockets func(context.Context) error
	if r.websockets != nil {
		stopWebSockets = r.websockets.stop
	}
	var httpRuntime transport.Server
	if r.http != nil {
		httpRuntime = withStopLifecycle(r.http, r.stopDelay, stopWebSockets, r.health)
	}
	var grpcRuntime transport.Server
	if r.grpc != nil {
		grpcRuntime = withStopLifecycle(r.grpc, r.stopDelay, nil, nil)
	}
	return httpRuntime, grpcRuntime
}

// withStopLifecycle 仅在需要停机前置动作或延迟时包装 Server，并保留 Endpointer 能力。
func withStopLifecycle(
	server transport.Server,
	delay time.Duration,
	beforeStop func(context.Context) error,
	health *healthState,
) transport.Server {
	if server == nil {
		return nil
	}
	if delay <= 0 && beforeStop == nil && health == nil {
		return server
	}

	runtime := &stopRuntime{
		Server:     server,
		health:     health,
		delay:      delay,
		beforeStop: beforeStop,
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
	// beforeStop 底层停止前的附加清理回调，可为 nil。
	beforeStop func(context.Context) error
}

type endpointStopRuntime struct {
	// stopRuntime 附加停机策略的服务包装。
	*stopRuntime
	// Endpointer 保留底层服务的端点查询能力。
	transport.Endpointer
}

// Stop 等待配置延迟后依次执行前置清理和底层 Server 停止，并合并所有错误。
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
			return errors.Join(ctx.Err(), s.runBeforeStop(ctx), s.Server.Stop(ctx))
		}
	}
	return errors.Join(s.runBeforeStop(ctx), s.Server.Stop(ctx))
}

// runBeforeStop 在没有前置清理时直接返回，避免每个协议构造空回调。
func (s *stopRuntime) runBeforeStop(ctx context.Context) error {
	if s.beforeStop == nil {
		return nil
	}
	return s.beforeStop(ctx)
}

// ManagementServers 返回独立监控监听的运行时；不实现 Endpointer，避免注册成业务发现地址。
// 使用 NewServerBootstrap 时内部自动登记；手工组装须与 Servers 返回值一起登记。
func (r *Runtime) ManagementServers() []transport.Server {
	result := make([]transport.Server, 0, len(r.management))
	for _, srv := range r.management {
		result = append(result, &managementRuntime{Server: withStopLifecycle(srv, r.stopDelay, nil, r.health)})
	}
	return result
}

// managementRuntime 仅公开启停能力，阻止管理端口进入服务注册的 endpoint 列表。
type managementRuntime struct {
	// Server 被委托启停的管理服务。
	transport.Server
}
