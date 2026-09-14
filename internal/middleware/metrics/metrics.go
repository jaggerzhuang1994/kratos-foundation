package metrics

import (
	"context"
	"fmt"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"

	"github.com/go-kratos/kratos/v2/middleware"
	metrics2 "github.com/go-kratos/kratos/v2/middleware/metrics"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是指标中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Metrics

const (
	meterName                  = "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metrics"
	serverSecondsHistogramName = "server_requests_seconds"
	clientSecondsHistogramName = "client_requests_seconds"
)

// Server 创建使用应用私有 Meter 的服务端指标中间件。
func Server(provider metrics.Provider, config Config) (middleware.Middleware, error) {
	if config.GetDisable() {
		return nil, nil
	}

	opts, err := newOpts(
		provider,
		metrics2.DefaultServerRequestsCounterName,
		serverSecondsHistogramName,
	)
	if err != nil {
		return nil, err
	}
	return withStatusCode(metrics2.Server(opts...)), nil
}

// Client 创建使用应用私有 Meter 的客户端指标中间件。
func Client(provider metrics.Provider, config Config) (middleware.Middleware, error) {
	if config.GetDisable() {
		return nil, nil
	}

	opts, err := newOpts(
		provider,
		metrics2.DefaultClientRequestsCounterName,
		clientSecondsHistogramName,
	)
	if err != nil {
		return nil, err
	}
	return withStatusCode(metrics2.Client(opts...)), nil
}

// newOpts 在启动期创建固定指标，确保非法名称不会延迟到首个请求才暴露。
func newOpts(
	provider metrics.Provider,
	counterName string,
	histogramName string,
) ([]metrics2.Option, error) {
	meter := provider.Meter(meterName)
	// 在启动阶段创建固定名称的指标，可以让命名冲突和非法配置尽早暴露，而不是等到
	// 第一个请求到来后才失败。
	requestsCounter, err := metrics2.DefaultRequestsCounter(meter, counterName)
	if err != nil {
		return nil, fmt.Errorf("create metrics middleware requests counter: %w", err)
	}
	secondsHistogram, err := metrics2.DefaultSecondsHistogram(meter, histogramName)
	if err != nil {
		return nil, fmt.Errorf("create metrics middleware seconds histogram: %w", err)
	}

	return []metrics2.Option{
		metrics2.WithRequests(requestsCounter),
		metrics2.WithSeconds(secondsHistogram),
	}, nil
}

// withStatusCode 为上游指标提供原始 HTTP 状态，避免 422 经 gRPC 映射后被统计为 500。
// 临时错误仅用于指标观察；调用方仍收到原始错误及其完整因果链。
func withStatusCode(observe middleware.Middleware) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			var original error
			measured := observe(func(ctx context.Context, req any) (any, error) {
				reply, err := next(ctx, req)
				original = err
				if err == nil {
					return reply, nil
				}
				state := foundationerrors.FromError(foundationerrors.Normalize(err))
				return reply, kratoserrors.New(int(state.Code), state.Reason, state.Message)
			})
			reply, err := measured(ctx, req)
			if original != nil {
				return reply, original
			}
			return reply, err
		}
	}
}
