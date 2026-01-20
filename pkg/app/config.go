package app

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.App
type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	return &config_pb.App{
		DisableRegistrar: proto.Bool(false),
		RegistrarTimeout: durationpb.New(10 * time.Second),
		StopTimeout:      durationpb.New(30 * time.Second),
		Endpoints:        nil,
		Metadata:         nil,
	}
}

func NewConfig(
	config config.KratosFoundationConfig,
	defaultConfig DefaultConfig,
) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetApp())
	return c
}
