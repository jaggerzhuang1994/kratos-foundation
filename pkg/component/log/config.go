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

var defaultLevel = DefaultLevel()

var defaultConfig = &Config{
	Level:       &defaultLevel,
	FilterEmpty: proto.Bool(true),
	FilterKeys:  []string{},
	TimeFormat:  proto.String(time.RFC3339),
	Std: &kratos_foundation_pb.LogComponentConfig_Log_StdLogger{
		Disable:     proto.Bool(false),
		Level:       nil, // default to log.level
		FilterEmpty: nil, // default to log.FilterEmpty
		FilterKeys:  []string{},
	},
	File: &kratos_foundation_pb.LogComponentConfig_Log_FileLogger{
		Disable:     proto.Bool(false),
		Level:       nil, // default to log.level
		FilterEmpty: nil, // default to log.FilterEmpty
		FilterKeys:  []string{},
		Path:        proto.String("./app.log"),
		Rotating: &kratos_foundation_pb.LogComponentConfig_Log_FileLogger_Rotating{
			Disable:    proto.Bool(false),
			MaxSize:    proto.Int64(100),
			MaxFileAge: proto.Int32(0),
			MaxFiles:   proto.Int32(0),
			LocalTime:  proto.Bool(false),
			Compress:   proto.Bool(false),
		},
	},
	Preset: nil, // 为空默认全部
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
