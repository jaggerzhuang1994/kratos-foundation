package metadata

import (
	metadata2 "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Metadata

func Enable(config *Config) bool {
	return !config.GetDisable()
}

func Server(config *Config) middleware.Middleware {
	opts := newMiddlewareOptions(config)
	return metadata.Server(opts...)
}

func Client(config *Config) middleware.Middleware {
	opts := newMiddlewareOptions(config)
	return metadata.Client(opts...)
}

func newMiddlewareOptions(configs ...*Config) []metadata.Option {
	var opts []metadata.Option
	// 合并去重 prefix
	prefix := utils.Unique(utils.Flat(utils.Map(configs, (*Config).GetPrefix)))
	if len(prefix) > 0 {
		opts = append(opts, metadata.WithPropagatedPrefix(prefix...))
	}
	// 合并 constants 后面的 Config 会覆盖前面的
	constantsList := utils.Map(configs, (*Config).GetConstants)
	constants := mergeConstantsMd(constantsList...)
	if len(constants) > 0 {
		opts = append(opts, metadata.WithConstants(constants))
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
