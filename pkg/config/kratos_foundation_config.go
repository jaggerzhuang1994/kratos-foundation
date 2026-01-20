package config

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
)

type KratosFoundationConfig = *kratos_foundation_pb.Config

func NewKratosFoundationConfig(
	config Config,
	logger log.UpdateLogger,
) (KratosFoundationConfig, error) {
	var c kratos_foundation_pb.Config
	err := config.Scan(&c)
	if err != nil {
		return nil, errors.WithMessage(err, "load KratosFoundationConfig failed")
	}
	err = logger.Update(c.GetLog())
	if err != nil {
		return nil, errors.WithMessage(err, "init log failed")
	}
	return &c, nil
}
