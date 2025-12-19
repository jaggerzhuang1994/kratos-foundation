package database

import (
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.Database

var defaultGormLoggerLevel = kratos_foundation_pb.Database_Gorm_Logger_WARN

var defaultConfig = &Config{
	Gorm: &kratos_foundation_pb.Database_Gorm{
		Logger: &kratos_foundation_pb.Database_Gorm_Logger{
			Level:                     &defaultGormLoggerLevel,
			SlowThreshold:             durationpb.New(200 * time.Millisecond),
			Colorful:                  proto.Bool(false), // 彩色输出
			IgnoreRecordNotFoundError: proto.Bool(true),  // 忽略不存在的记录的错误信息
			ParameterizedQueries:      proto.Bool(false), //
		},
	},
	Default: proto.String("default"),
	Tracing: &kratos_foundation_pb.Database_Tracing{
		Disable:                proto.Bool(false),
		ExcludeQueryVars:       proto.Bool(false),
		ExcludeMetrics:         proto.Bool(false),
		RecordStackTraceInSpan: proto.Bool(false),
		ExcludeServerAddress:   proto.Bool(false),
	},
}

func NewConfig(conf *kratos_foundation_pb.Config) *Config {
	c := proto.CloneOf(defaultConfig)
	proto.Merge(c, conf.GetDatabase())
	return c
}
