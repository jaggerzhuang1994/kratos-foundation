package database

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.Database

type DefaultConfig Config

func NewDefaultConfig() DefaultConfig {
	defaultGormLoggerLevel := config_pb.GormLogger_SILENT

	return &config_pb.Database{
		Gorm: &config_pb.Gorm{
			Logger: &config_pb.GormLogger{
				Level:                     &defaultGormLoggerLevel,
				SlowThreshold:             durationpb.New(200 * time.Millisecond),
				Colorful:                  proto.Bool(false), // 彩色输出
				IgnoreRecordNotFoundError: proto.Bool(true),  // 忽略不存在的记录的错误信息
				ParameterizedQueries:      proto.Bool(false), //
			},
		},
		Default: proto.String("default"),
	}
}

func NewConfig(
	config config.KratosFoundationConfig,
	defaultConfig DefaultConfig,
) Config {
	c := proto.CloneOf((Config)(defaultConfig))
	proto.Merge(c, config.GetDatabase())
	return c
}
