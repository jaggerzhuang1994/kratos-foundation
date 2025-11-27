package client

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.ClientComponentConfig_Client
type ClientOption = kratos_foundation_pb.ClientComponentConfig_Client_ClientOption

var defaultConfig = &Config{}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.ClientComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan ClientComponentConfig failed")
	}

	dbConfig := proto.CloneOf(defaultConfig)
	proto.Merge(dbConfig, scc.GetClient())

	return dbConfig, nil
}
