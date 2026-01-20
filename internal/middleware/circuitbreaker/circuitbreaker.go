package circuitbreaker

import (
	circuitbreaker2 "github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

type Config = *config_pb.Middleware_CircuitBreaker

func Client(config Config) middleware.Middleware {
	if !config.GetEnable() {
		return nil
	}
	return circuitbreaker.Client(circuitbreaker.WithCircuitBreaker(func() circuitbreaker2.CircuitBreaker {
		return NewSREBreaker(config.GetSre())
	}))
}

func NewSREBreaker(config *config_pb.Middleware_CircuitBreaker_SREBreaker) circuitbreaker2.CircuitBreaker {
	var opts []sre.Option

	if config != nil {
		if config.Success != nil {
			opts = append(opts, sre.WithSuccess(config.GetSuccess()))
		}

		if config.Request != nil {
			opts = append(opts, sre.WithRequest(config.GetRequest()))
		}

		if config.Bucket != nil {
			opts = append(opts, sre.WithBucket(int(config.GetBucket())))
		}

		if config.Window != nil {
			opts = append(opts, sre.WithWindow(config.GetWindow().AsDuration()))
		}
	}

	return sre.NewBreaker(opts...)
}
