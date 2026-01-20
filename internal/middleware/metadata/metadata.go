package metadata

import (
	metadata2 "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

type Config = *config_pb.Middleware_Metadata

func Server(config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	opts := newMiddlewareOptions(config)
	return server(opts...)
}

func Client(config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	opts := newMiddlewareOptions(config)
	return client(opts...)
}

func newMiddlewareOptions(configs ...Config) []Option {
	var opts []Option
	// 合并去重 prefix
	prefix := utils.Unique(utils.Flat(utils.Map(configs, (*config_pb.Middleware_Metadata).GetPrefix)))
	if len(prefix) > 0 {
		opts = append(opts, withPropagatedPrefix(prefix...))
	}
	// 合并 constants 后面的 Config 会覆盖前面的
	constantsList := utils.Map(configs, (*config_pb.Middleware_Metadata).GetConstants)
	constants := mergeConstantsMd(constantsList...)
	if len(constants) > 0 {
		opts = append(opts, withConstants(constants))
	}
	return opts
}

// 合并 constants md
func mergeConstantsMd(constantsList ...map[string]string) metadata2.Metadata {
	md := metadata2.Metadata{}

	for _, constants := range constantsList {
		for k, v := range constants {
			md.Add(k, v)
		}
	}

	return md
}
