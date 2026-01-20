package discovery

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.Discovery

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	dc := config_pb.DC_SINGLE
	return &config_pb.Discovery{
		Timeout: durationpb.New(time.Second * 10),
		Dc:      &dc,
	}
}

func NewConfig(config config.KratosFoundationConfig, defaultConfig DefaultConfig) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetDiscovery())
	return c
}
