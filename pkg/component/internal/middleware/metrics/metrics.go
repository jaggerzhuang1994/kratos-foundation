package metrics

import (
	"github.com/go-kratos/kratos/v2/middleware"
	metrics2 "github.com/go-kratos/kratos/v2/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
)

type Metrics = metrics.Metrics

type Config = kratos_foundation_pb.MiddlewareConfig_Metrics

func Enable(config *Config) bool {
	return !config.GetDisable()
}

func Server(metrics *metrics.Metrics, config *Config) (middleware.Middleware, error) {
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

func Client(metrics *metrics.Metrics, config *Config) (middleware.Middleware, error) {
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

func newOpts(metrics *metrics.Metrics, counterName, histogramName string, configs ...*Config) ([]metrics2.Option, error) {

	// 按照 configs 倒序查找第一个不为空的 meter_name
	// 兜底为 metrics.GetDefaultMeterName()
	meterName := utils.Select(
		append(utils.Map(utils.Reverse(configs), (*Config).GetMeterName), metrics.GetDefaultMeterName())...,
	)
	meter := metrics.GetMeterProvider().Meter(meterName)
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
