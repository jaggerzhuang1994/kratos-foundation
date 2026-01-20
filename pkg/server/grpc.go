package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
)

type GrpcServer = *grpc.Server

// NewGrpcServer 默认 grpc 服务器
func NewGrpcServer(
	config Config,
	opts GrpcServerOptions,
) GrpcServer {
	if config.GetGrpc().GetDisable() {
		return nil
	}
	srv := grpc.NewServer(opts...)
	return srv
}

type GrpcServerOptions []grpc.ServerOption

func NewGrpcServerOptions(config Config, middleware Middlewares) (opts GrpcServerOptions) {
	conf := config.GetGrpc()
	// 监听（"tcp", "tcp4", "tcp6", "unix" or "unixpacket"）
	if conf.GetNetwork() != "" {
		opts = append(opts, grpc.Network(conf.GetNetwork()))
	}
	// 监听的host:port
	if conf.GetAddr() != "" {
		opts = append(opts, grpc.Address(conf.GetAddr()))
	}
	// 设置 grpc 对外暴露的端点
	if conf.GetEndpoint() != nil {
		opts = append(opts, grpc.Endpoint(&url.URL{Scheme: conf.GetEndpoint().GetScheme(), Host: conf.GetEndpoint().GetHost()}))
	}
	// 使用中间件来控制超时 需要显式设置为 0，否则内部会有默认值1s
	opts = append(opts, grpc.Timeout(0))
	if conf.GetCustomHealth() {
		opts = append(opts, grpc.CustomHealth())
	}
	if conf.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	opts = append(opts, grpc.Middleware(middleware...))
	return opts
}
