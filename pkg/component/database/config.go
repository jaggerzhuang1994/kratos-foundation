package database

import (
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = kratos_foundation_pb.DatabaseComponentConfig_Database

var defaultGormLogger = &kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm_Logger{
	Level:                     kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm_Logger_Warn,
	SlowThreshold:             durationpb.New(200 * time.Millisecond),
	Colorful:                  proto.Bool(false), // 彩色输出
	IgnoreRecordNotFoundError: proto.Bool(true),  // 忽略不存在的记录的错误信息
	ParameterizedQueries:      proto.Bool(false), //
}

var defaultConfig = &Config{
	Default: "default",
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.DatabaseComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan DatabaseComponentConfig failed")
	}

	dbConfig := proto.CloneOf(defaultConfig)
	proto.Merge(dbConfig, scc.GetDatabase())

	return dbConfig, nil
}
