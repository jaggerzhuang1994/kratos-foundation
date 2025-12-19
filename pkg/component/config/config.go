package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.Config

func NewConfig(conf config.Config) (*Config, error) {
	var c Config
	err := conf.Scan(&c)
	if err != nil {
		return nil, errors.WithMessage(err, "load Config failed: ")
	}

	return &c, nil
}

func Merge(dst, src proto.Message) {
	proto.Merge(dst, src)
}
