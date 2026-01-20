package server

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.Server

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	return &config_pb.Server{
		StopDelay:  durationpb.New(0),
		Middleware: nil,
		Http: &config_pb.HttpServerOption{
			Disable:            proto.Bool(false),
			Network:            proto.String("tcp"),
			Addr:               proto.String("0.0.0.0:8000"),
			Endpoint:           nil,
			DisableStrictSlash: proto.Bool(false),
			PathPrefix:         proto.String(""),
			Metrics: &config_pb.HttpServerOption_Metrics{
				Disable: proto.Bool(false),
				Path:    proto.String("/metrics"),
			},
		},
		Grpc: &config_pb.GrpcServerOption{
			Disable:           proto.Bool(false),
			Network:           proto.String("tcp"),
			Addr:              proto.String("0.0.0.0:9000"),
			Endpoint:          nil,
			CustomHealth:      proto.Bool(false),
			DisableReflection: proto.Bool(false),
		},
		Log: nil,
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetServer())
	return c
}
