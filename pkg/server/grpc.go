package server

import (
	"fmt"
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/requestdebug"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// GRPCServer 是 Foundation service 使用的 Kratos gRPC Server。
type GRPCServer = *grpc.Server

// newGRPCServer 在启动期完成服务注册，避免运行后才暴露不完整服务。
// 未配置 custom_health 且业务未注册健康服务时，注册与 HTTP 探针共享就绪状态的健康服务。
func newGRPCServer(
	config componentConfig,
	opts grpcServerOptions,
	spec *Spec,
	health *healthState,
) (GRPCServer, error) {
	// 显式配置优先；省略开关时仅有效的业务服务注册触发默认启用。
	conf := config.GetGrpc()
	if conf.GetDisable() || ((conf == nil || conf.Disable == nil) && len(spec.grpc.services) == 0) {
		return nil, nil
	}

	// Kratos 内置健康服务在 Start 后即报 SERVING，与应用就绪脱钩；一律关闭，由 Foundation 或业务（custom_health）提供。
	srv := grpc.NewServer(append(append(grpcServerOptions(nil), opts...), grpc.CustomHealth())...)
	for _, service := range spec.grpc.services {
		if err := service(srv); err != nil {
			return nil, fmt.Errorf("register gRPC service: %w", err)
		}
	}
	// 配置 custom_health 或业务已自行注册健康服务（例如经原生 CustomHealth option）时交由业务处理。
	if _, registered := srv.GetServiceInfo()[healthpb.Health_ServiceDesc.ServiceName]; !conf.GetCustomHealth() && !registered {
		healthpb.RegisterHealthServer(srv, &grpcHealth{state: health, services: srv.GetServiceInfo})
	}
	return srv, nil
}

// GRPCServerOptions 聚合构造 GRPCServer 所需的 Kratos option。
type grpcServerOptions []grpc.ServerOption

// newGRPCServerOptions 让业务 option 最后应用，以保留显式覆盖能力。
func newGRPCServerOptions(
	config componentConfig,
	middlewares middlewareSet,
	spec *Spec,
) grpcServerOptions {
	conf := config.GetGrpc()
	var opts grpcServerOptions
	if conf.GetNetwork() != "" {
		opts = append(opts, grpc.Network(conf.GetNetwork()))
	}
	if conf.GetAddr() != "" {
		opts = append(opts, grpc.Address(conf.GetAddr()))
	}
	if conf.GetEndpoint() != nil {
		opts = append(opts, grpc.Endpoint(&url.URL{
			Scheme: conf.GetEndpoint().GetScheme(),
			Host:   conf.GetEndpoint().GetHost(),
		}))
	}
	opts = append(opts, grpc.Timeout(0))
	if conf.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	// 流需要在建立时恢复状态；复用相同的动态 debug 策略和按名替换规则。
	var debugEntries middlewareSet
	for _, entry := range middlewares {
		if entry.Name == "request_debug" {
			debugEntries = append(debugEntries, entry)
		}
	}
	var debugOverrides []MiddlewareSpec
	for _, entry := range spec.grpc.middlewares {
		if entry.Name == "request_debug" {
			debugOverrides = append(debugOverrides, entry)
		}
	}
	for _, debug := range debugEntries.build(debugOverrides) {
		opts = append(opts, grpc.StreamInterceptor(requestdebug.StreamServer(debug)))
	}
	opts = append(opts, spec.grpc.options...)
	opts = append(opts, grpc.Middleware(middlewares.build(spec.grpc.middlewares)...))
	return opts
}
