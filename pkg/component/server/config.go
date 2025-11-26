package server

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.ServerComponentConfig_Server

var defaultConfig = &Config{
	StopDelay:  durationpb.New(0),
	Middleware: nil,
	Http: &kratos_foundation_pb.ServerComponentConfig_HttpServerOption{
		Disable:            false,
		Network:            "tcp",
		Addr:               "0.0.0.0:8000",
		Endpoint:           nil, // 默认使用服务暴露的 host:port
		Timeout:            durationpb.New(1 * time.Second),
		Middleware:         nil,
		DisableStrictSlash: false,
		PathPrefix:         "",
		Metrics: &kratos_foundation_pb.ServerComponentConfig_HttpServerOption_Metrics{
			Disable: false,
			Path:    "/metrics",
		},
	},
	Grpc: &kratos_foundation_pb.ServerComponentConfig_GrpcServerOption{
		Disable:           false,
		Network:           "tcp",
		Addr:              "0.0.0.0:9000",
		Endpoint:          nil, // 默认使用服务暴露的 host:port
		Timeout:           durationpb.New(1 * time.Second),
		Middleware:        nil,
		CustomHealth:      false,
		DisableReflection: false,
	},
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.ServerComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan ServerComponentConfig failed")
	}

	serverConfig := proto.CloneOf(defaultConfig)
	proto.Merge(serverConfig, scc.GetServer())

	return serverConfig, nil
}
