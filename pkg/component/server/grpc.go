package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type GrpcServerOptions []grpc.ServerOption

const grpcServerLogModule = "server.grpc"

func NewGrpcServer(
	_ bootstrap.Bootstrap,
	cfg *Config,
	log *log.Log,
	metrics *metric.Metrics,
	tracing *tracing.Tracing,
	hook *HookManager,
) *grpc.Server {
	if cfg.GetGrpc().GetDisable() {
		return nil
	}
	log = log.WithModule(grpcServerLogModule, cfg.GetLog())

	middlewares := middleware.NewServerMiddleware(log, metrics, tracing, cfg.GetMiddleware(), cfg.GetGrpc().GetMiddleware())
	for _, grpcServerMiddleware := range hook.grpcServerMiddlewares {
		middlewares = grpcServerMiddleware(middlewares)
	}

	opts := newGrpcServerOptions(cfg, middlewares)
	for _, hookGrpcServerOption := range hook.grpcServerOptions {
		opts = hookGrpcServerOption(opts)
	}

	srv := grpc.NewServer(opts...)
	return srv
}

func newGrpcServerOptions(cfg *kratos_foundation_pb.ServerComponentConfig_Server, middlewares middleware.Middlewares) GrpcServerOptions {
	grpcCfg := cfg.GetGrpc()
	var opts GrpcServerOptions
	// 监听（"tcp", "tcp4", "tcp6", "unix" or "unixpacket"）
	if grpcCfg.GetNetwork() != "" {
		opts = append(opts, grpc.Network(grpcCfg.GetNetwork()))
	}
	// 监听的host:port
	if grpcCfg.GetAddr() != "" {
		opts = append(opts, grpc.Address(grpcCfg.GetAddr()))
	}
	// 设置http对外暴露的端点
	if grpcCfg.GetEndpoint() != nil {
		opts = append(opts, grpc.Endpoint(&url.URL{Scheme: grpcCfg.GetEndpoint().GetScheme(), Host: grpcCfg.GetEndpoint().GetHost()}))
	}
	// 设置http接口的超时时间
	if grpcCfg.GetTimeout() != nil {
		opts = append(opts, grpc.Timeout(grpcCfg.GetTimeout().AsDuration()))
	}
	if grpcCfg.GetCustomHealth() {
		opts = append(opts, grpc.CustomHealth())
	}
	if grpcCfg.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	if len(middlewares) > 0 {
		opts = append(opts, grpc.Middleware(middlewares...))
	}
	return opts
}
