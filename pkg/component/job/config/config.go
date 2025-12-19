package config

import (
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.Job

var defaultConfig = &Config{}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetJob())
	return c
}
