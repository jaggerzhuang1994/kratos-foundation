package server

import (
	"fmt"
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
)

// GRPCServer 是 Foundation service 使用的 Kratos gRPC Server。
type GRPCServer = *grpc.Server

// newGRPCServer 在启动期完成服务注册，避免运行后才暴露不完整服务。
func newGRPCServer(
	config componentConfig,
	opts grpcServerOptions,
	spec *Spec,
) (GRPCServer, error) {
	if spec.grpcDisabled(config.GetGrpc().GetDisable()) {
		return nil, nil
	}

	srv := grpc.NewServer(opts...)
	for _, service := range spec.grpc.services {
		if err := service(srv); err != nil {
			return nil, fmt.Errorf("register gRPC service: %w", err)
		}
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
	if conf.GetCustomHealth() {
		opts = append(opts, grpc.CustomHealth())
	}
	if conf.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	opts = append(opts, spec.grpc.options...)
	opts = append(opts, grpc.Middleware(middlewares.build(spec.grpc.middlewares)...))
	return opts
}
