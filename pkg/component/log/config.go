package log

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.LogComponentConfig_Log

type ModuleConfig interface {
	GetLevel() string
	GetFilterKeys() []string
}

var defaultConfig = &Config{
	Level: proto.String(DefaultLevel()),
	File: &kratos_foundation_pb.LogComponentConfig_Log_FileLogger{
		Path: proto.String("./app.log"),
	},
	TimeFormat: proto.String(time.RFC3339),
}

func DefaultLevel() string {
	var level = "info"
	if env.AppDebug() || env.IsLocal() {
		level = "debug"
	}
	return level
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.LogComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan LogComponentConfig failed")
	}

	logConfig := proto.CloneOf(defaultConfig)
	proto.Merge(logConfig, scc.GetLog())

	return logConfig, nil
}
