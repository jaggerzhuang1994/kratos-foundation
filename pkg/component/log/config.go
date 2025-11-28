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
	Level:       DefaultLevel(),
	FilterKeys:  []string{},
	FilterEmpty: false,
	TimeFormat:  time.RFC3339,
	Std: &kratos_foundation_pb.LogComponentConfig_Log_StdLogger{
		Disable:     false,
		Level:       nil, // default to log.level
		FilterEmpty: nil, // default to log.FilterEmpty
		FilterKeys:  []string{},
	},
	File: &kratos_foundation_pb.LogComponentConfig_Log_FileLogger{
		Disable:     false,
		Level:       nil, // default to log.level
		FilterEmpty: nil, // default to log.FilterEmpty
		FilterKeys:  []string{},
		Path:        "./app.log",
		Rotating: &kratos_foundation_pb.LogComponentConfig_Log_FileLogger_Rotating{
			Disable:    false,
			MaxSize:    100,
			MaxFileAge: 0,
			MaxFiles:   0,
			LocalTime:  false,
			Compress:   false,
		},
	},
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
