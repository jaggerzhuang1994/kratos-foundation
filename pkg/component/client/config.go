package client

import (
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.Client
type ClientOption = kratos_foundation_pb.Client_ClientOption

var defaultConfig = &Config{}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetClient())
	return c
}
