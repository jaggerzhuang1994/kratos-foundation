package app

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.App

var defaultConfig = &Config{
	DisableRegistrar: proto.Bool(false),
	RegistrarTimeout: durationpb.New(10 * time.Second),
	StopTimeout:      durationpb.New(30 * time.Second),
}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetApp())
	return c
}
