package server

import (
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.Server

var defaultConfig = &Config{
	StopDelay:  durationpb.New(0),
	Middleware: nil,
	Http: &kratos_foundation_pb.Server_HttpServerOption{
		Disable:            proto.Bool(false),
		Network:            proto.String("tcp"),
		Addr:               proto.String("0.0.0.0:8000"),
		Endpoint:           nil, // 默认使用服务暴露的 host:port
		DisableStrictSlash: proto.Bool(false),
		PathPrefix:         proto.String(""),
		Metrics: &kratos_foundation_pb.Server_HttpServerOption_Metrics{
			Disable: proto.Bool(false),
			Path:    proto.String("/metrics"),
		},
	},
	Grpc: &kratos_foundation_pb.Server_GrpcServerOption{
		Disable:           proto.Bool(false),
		Network:           proto.String("tcp"),
		Addr:              proto.String("0.0.0.0:9000"),
		Endpoint:          nil, // 默认使用服务暴露的 host:port
		CustomHealth:      proto.Bool(false),
		DisableReflection: proto.Bool(false),
	},
}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetServer())
	return c
}
