package ratelimit

import (
	ratelimit2 "github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Ratelimit

func Enable(config *Config) bool {
	return config.GetEnable()
}

func Server(config *Config) middleware.Middleware {
	return ratelimit.Server(ratelimit.WithLimiter(NewBBRLimiter(config.GetBbrLimiter())))
}

func NewBBRLimiter(bbrCfg *kratos_foundation_pb.MiddlewareConfig_Ratelimit_BBRLimiter) ratelimit2.Limiter {
	var opts []bbr.Option

	if bbrCfg != nil {
		if bbrCfg.Window != nil {
			opts = append(opts, bbr.WithWindow(bbrCfg.GetWindow().AsDuration()))
		}

		if bbrCfg.Bucket != nil {
			opts = append(opts, bbr.WithBucket(int(bbrCfg.GetBucket())))
		}

		if bbrCfg.CpuThreshold != nil {
			opts = append(opts, bbr.WithCPUThreshold(bbrCfg.GetCpuThreshold()))
		}

		if bbrCfg.CpuQuota != nil {
			opts = append(opts, bbr.WithCPUQuota(bbrCfg.GetCpuQuota()))
		}
	}

	return bbr.NewLimiter(opts...)
}
