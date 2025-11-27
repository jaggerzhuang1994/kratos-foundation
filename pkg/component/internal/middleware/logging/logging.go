package logging

import (
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Logging

func Enable(config *Config) bool {
	return !config.GetDisable()
}

func Server(log *log.Log, _ *Config) middleware.Middleware {
	return logging.Server(log.GetLogger())
}

func Client(log *log.Log, _ *Config) middleware.Middleware {
	return logging.Client(log.GetLogger())
}
