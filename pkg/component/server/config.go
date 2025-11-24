package server

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.ServerComponentConfig_Server

var defaultConfig = &Config{}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.ServerComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan ServerComponentConfig failed")
	}

	serverConfig := proto.CloneOf(defaultConfig)
	proto.Merge(serverConfig, scc.GetServer())

	return serverConfig, nil
}
