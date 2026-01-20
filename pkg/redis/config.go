package redis

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type Config = *config_pb.Redis
type Option = *config_pb.RedisOption

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	return &config_pb.Redis{
		Default:     proto.String("default"),
		Connections: nil,
		Log:         nil,
		Tracing: &config_pb.RedisTracing{
			DbStatement:   proto.Bool(true),
			CallerEnabled: proto.Bool(true),
			DialFilter:    proto.Bool(true),
		},
		Metrics: nil,
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetRedis())
	return c
}
