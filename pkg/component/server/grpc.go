package server

import (
	"net/url"

	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

// NewGrpcServer 默认 grpc 服务器
func NewGrpcServer(
	cfg *Config,
	register *Register,
	hook *Hook,
) *grpc.Server {
	if cfg.GetGrpc().GetDisable() {
		return nil
	}

	opts := newGrpcServerOptions(cfg)
	opts = append(opts, grpc.Middleware(hook.middleware...))

	srv := grpc.NewServer(opts...)
	// hook回调
	for _, fn := range hook.grpcServer {
		fn(srv)
	}
	register.RegisterServer(srv)
	return srv
}

func newGrpcServerOptions(cfg *kratos_foundation_pb.ServerComponentConfig_Server) []grpc.ServerOption {
	grpcCfg := cfg.GetGrpc()
	var opts []grpc.ServerOption
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
	//// 设置grpc接口的超时时间
	//if grpcCfg.GetTimeout() != nil {
	//	opts = append(opts, grpc.Timeout(grpcCfg.GetTimeout().AsDuration()))
	//}
	// 使用中间件来控制超时 需要显式设置为 0，否则内部会有默认值1s
	opts = append(opts, grpc.Timeout(0))
	if grpcCfg.GetCustomHealth() {
		opts = append(opts, grpc.CustomHealth())
	}
	if grpcCfg.GetDisableReflection() {
		opts = append(opts, grpc.DisableReflection())
	}
	return opts
}
