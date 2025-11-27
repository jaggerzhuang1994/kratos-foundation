package circuitbreaker

import (
	circuitbreaker2 "github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

type Config = kratos_foundation_pb.MiddlewareConfig_Circuitbreaker

func Enable(config *Config) bool {
	return config.GetEnable()
}

func Client(config *Config) middleware.Middleware {
	return circuitbreaker.Client(circuitbreaker.WithCircuitBreaker(func() circuitbreaker2.CircuitBreaker {
		return NewSREBreaker(config.GetSre())
	}))
}

func NewSREBreaker(sreCfg *kratos_foundation_pb.MiddlewareConfig_Circuitbreaker_SREBreaker) circuitbreaker2.CircuitBreaker {
	var opts []sre.Option

	if sreCfg != nil {
		if sreCfg.Success != nil {
			opts = append(opts, sre.WithSuccess(sreCfg.GetSuccess()))
		}

		if sreCfg.Request != nil {
			opts = append(opts, sre.WithRequest(sreCfg.GetRequest()))
		}

		if sreCfg.Bucket != nil {
			opts = append(opts, sre.WithBucket(int(sreCfg.GetBucket())))
		}

		if sreCfg.Window != nil {
			opts = append(opts, sre.WithWindow(sreCfg.GetWindow().AsDuration()))
		}
	}

	return sre.NewBreaker(opts...)
}
