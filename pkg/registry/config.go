package registry

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.Registry

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	return &config_pb.Registry{
		DisableHealthCheck:             proto.Bool(false),
		DisableHeartbeat:               proto.Bool(false),
		HealthcheckInternal:            durationpb.New(time.Second * 10),
		DeregisterCriticalServiceAfter: durationpb.New(time.Second * 600),
		Tags:                           nil,
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetRegistry())
	return c
}
