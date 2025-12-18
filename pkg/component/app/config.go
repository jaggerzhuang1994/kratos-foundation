package app

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.AppComponentConfig_App

var defaultConfig = &Config{
	DisableRegistrar: proto.Bool(false),
	RegistrarTimeout: durationpb.New(10 * time.Second),
	StopTimeout:      durationpb.New(30 * time.Second),
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.AppComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan AppComponentConfig failed")
	}

	appConfig := proto.CloneOf(defaultConfig)
	proto.Merge(appConfig, scc.GetApp())

	return appConfig, nil
}
