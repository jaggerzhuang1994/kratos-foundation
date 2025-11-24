package redis

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type Config = kratos_foundation_pb.RedisComponentConfig_RedisConfig
type RedisOption = kratos_foundation_pb.RedisComponentConfig_RedisConfig_RedisOption

var defaultConfig = &Config{
	Default: "default",
	Tracing: &kratos_foundation_pb.RedisComponentConfig_RedisConfig_Tracing{
		DbStatement:   true,
		CallerEnabled: true,
	},
}

func NewConfig(cfg config.Config) (*Config, error) {
	var scc kratos_foundation_pb.RedisComponentConfig
	err := cfg.Scan(&scc)
	if err != nil {
		return nil, errors.WithMessage(err, "scan RedisComponentConfig failed")
	}

	redisConfig := proto.CloneOf(defaultConfig)
	proto.Merge(redisConfig, scc.GetRedis())

	return redisConfig, nil
}
