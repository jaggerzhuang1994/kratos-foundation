package metrics

import (
	"github.com/go-kratos/kratos/v2/middleware"
	metrics2 "github.com/go-kratos/kratos/v2/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"github.com/pkg/errors"
)

type Metrics = metrics.Metrics

type Config = *config_pb.Middleware_Metrics

func Server(metrics metrics.Metrics, config Config) (middleware.Middleware, error) {
	if config.GetDisable() {
		return nil, nil
	}

	opts, err := newOpts(
		metrics,
		metrics2.DefaultServerRequestsCounterName,
		metrics2.DefaultServerSecondsHistogramName,
		config,
	)
	if err != nil {
		return nil, err
	}
	return metrics2.Server(opts...), nil
}

func Client(metrics metrics.Metrics, config Config) (middleware.Middleware, error) {
	if config.GetDisable() {
		return nil, nil
	}

	opts, err := newOpts(
		metrics,
		metrics2.DefaultClientRequestsCounterName,
		metrics2.DefaultClientSecondsHistogramName,
		config,
	)
	if err != nil {
		return nil, err
	}
	return metrics2.Client(opts...), nil
}

func newOpts(metrics metrics.Metrics, counterName, histogramName string, _ ...Config) ([]metrics2.Option, error) {
	meter := metrics.GetMeter()
	// server中间件指标初始化
	requestsCounter, err := metrics2.DefaultRequestsCounter(meter, counterName)
	if err != nil {
		return nil, errors.Wrap(err, "new MetricsMiddlewareRequestsCounter failed")
	}
	secondsHistogram, err := metrics2.DefaultSecondsHistogram(meter, histogramName)
	if err != nil {
		return nil, errors.Wrap(err, "new MetricsMiddlewareSecondsHistogram failed")
	}

	return []metrics2.Option{
		metrics2.WithRequests(requestsCounter),
		metrics2.WithSeconds(secondsHistogram),
	}, nil
}
