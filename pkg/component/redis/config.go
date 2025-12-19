package redis

import (
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.Redis
type RedisOption = kratos_foundation_pb.Redis_RedisOption

var defaultConfig = &Config{
	Default: proto.String("default"),
	Tracing: &kratos_foundation_pb.Redis_Tracing{
		DbStatement:   proto.Bool(true),
		CallerEnabled: proto.Bool(true),
	},
}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetRedis())
	return c
}
