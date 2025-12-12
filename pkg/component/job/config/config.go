package config

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.JobComponent_JobConfig

var defaultConfig = &Config{}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.JobComponent
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan JobComponent failed")
	}

	conf := proto.CloneOf(defaultConfig)
	proto.Merge(conf, scc.GetJob())

	return conf, nil
}
