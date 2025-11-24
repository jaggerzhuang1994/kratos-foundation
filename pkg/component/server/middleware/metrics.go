package middleware

import (
	"github.com/go-kratos/kratos/v2/middleware"
	metrics2 "github.com/go-kratos/kratos/v2/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
)

func Metrics(logger *log.Helper, metrics *metric.Metrics, meterName string) middleware.Middleware {
	meter := metrics.GetMeterProvider().Meter(meterName)
	// server中间件指标初始化
	serverRequestsCounter, err := metrics2.DefaultRequestsCounter(meter, metrics2.DefaultServerRequestsCounterName)
	if err != nil {
		logger.Warn(err, "new serverRequestsCounter failed")
		return nil
	}
	serverSecondsHistogram, err := metrics2.DefaultSecondsHistogram(meter, metrics2.DefaultServerSecondsHistogramName)
	if err != nil {
		logger.Warn(err, "new serverSecondsHistogram failed")
		return nil
	}

	return metrics2.Server(
		metrics2.WithSeconds(serverSecondsHistogram),
		metrics2.WithRequests(serverRequestsCounter),
	)
}
