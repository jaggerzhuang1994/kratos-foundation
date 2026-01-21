package log

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type Config = *config_pb.Log

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	var defaultLevel = "info"
	if env.AppDebug() || env.IsLocal() {
		defaultLevel = "debug"
	}
	return &config_pb.Log{
		Level:       proto.String(defaultLevel),
		FilterEmpty: proto.Bool(true),
		FilterKeys:  []string{},
		TimeFormat:  proto.String(time.RFC3339),
		Std: &config_pb.StdLogger{
			Disable: proto.Bool(false),
			Level:   proto.String(defaultLevel),
			FilterKeys: []string{
				"service.id", "service.name", "service.version",
			},
		},
		File: &config_pb.FileLogger{
			Disable:    proto.Bool(false),
			Level:      proto.String(defaultLevel),
			FilterKeys: nil,
			Path:       proto.String("./app.log"),
			Rotating: &config_pb.FileRotating{
				Disable:    proto.Bool(false),
				MaxSize:    proto.Int64(100),
				MaxFileAge: proto.Int32(0),
				MaxFiles:   proto.Int32(0),
				LocalTime:  proto.Bool(false),
				Compress:   proto.Bool(false),
			},
		},
		Preset: []string{}, // 空表示所有
	}
}
