package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/selector"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/filter"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/logging"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/timeout"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/internal/middleware/tracing"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

var ErrDiscoveryNotInitialized = errors.New("discovery not initialized")
var ErrParseTargetFailed = errors.New("parse target failed")

func (f *Factory) newGrpcClient(
	ctx context.Context,
	key clientKey,
) (GrpcClient, error) {
	var opts []grpc.ClientOption

	target := key.option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", key.name)
	}
	// 如果使用服务发现
	if strings.HasPrefix(target, "discovery://") {
		discoveryOpts, err := f.useGrpcDiscovery(target, key)
		if err != nil {
			return nil, err
		}
		opts = append(opts, discoveryOpts...)
	} else {
		// 使用直连
		opts = append(opts, grpc.WithEndpoint(target))
	}

	// 显式设置 timeout = 0，使用 timeout 中间件控制超时
	opts = append(opts, grpc.WithTimeout(0))

	// 中间件
	m := f.useMiddlewares(key)
	opts = append(opts, grpc.WithMiddleware(m...))

	conn, err := grpc.DialInsecure(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (f *Factory) newHttpClient(ctx context.Context, key clientKey) (HttpClient, error) {
	var opts []http.ClientOption

	target := key.option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", key.name)
	}

	// 如果使用服务发现
	if strings.HasPrefix(target, "discovery://") {
		discoveryOpts, err := f.useHttpDiscovery(target, key)
		if err != nil {
			return nil, err
		}
		opts = append(opts, discoveryOpts...)
	} else {
		// 使用直连
		opts = append(opts, http.WithEndpoint(target))
	}

	// 显式设置 timeout = 0，使用 timeout 中间件控制超时
	opts = append(opts, http.WithTimeout(0))

	// 中间件
	m := f.useMiddlewares(key)
	opts = append(opts, http.WithMiddleware(m...))

	conn, err := http.NewClient(
		ctx, opts...,
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (f *Factory) useMiddlewares(key clientKey) []middleware.Middleware {
	var m []middleware.Middleware
	var list []string

	config := key.option.GetMiddleware()

	// 超时中间件
	m = append(m, timeout.Client(
		f.log,
		config.GetTimeout(),
	))
	list = append(list, "timeout")

	// metadata
	if metadata.Enable(config.GetMetadata()) {
		m = append(m, metadata.Client(config.GetMetadata()))
		list = append(list, "metadata")
	}

	// tracing
	if tracing.Enable(config.GetTracing()) {
		m = append(m, tracing.Server(f.tracing, config.GetTracing()))
		list = append(list, "tracing")
	}

	// 监控中间件
	if metrics.Enable(config.GetMetrics()) {
		metricsMiddleware, err := metrics.Client(f.metrics, config.GetMetrics())
		if err != nil {
			log.Warn("Failed to create metrics middleware", zap.Error(err))
		} else {
			m = append(m, metricsMiddleware)
			list = append(list, "metrics")
		}
	}

	// 日志中间件
	if logging.Enable(config.GetLogging()) {
		m = append(m, logging.Client(f.log2, config.GetLogging()))
		list = append(list, "logging")
	}

	// 熔断器
	if circuitbreaker.Enable(config.GetCircuitbreaker()) {
		m = append(m, circuitbreaker.Client(config.GetCircuitbreaker()))
		list = append(list, "circuitbreaker")
	}

	f.log.Infof("client %s use middlewares %v", key.name, list)
	return m
}

func (f *Factory) useGrpcDiscovery(target string, key clientKey) ([]grpc.ClientOption, error) {
	if f.discovery == nil {
		return nil, ErrDiscoveryNotInitialized
	}

	targetUrl, err := url.Parse(target)
	if err != nil {
		return nil, errors.Wrap(err, ErrParseTargetFailed.Error())
	}

	// 默认 env 和 grpc 协议的过滤器
	var nodeFilters = []selector.NodeFilter{
		filter.Env(),
	}
	if key.isSecurity() {
		nodeFilters = append(nodeFilters, filter.Grpcs())
	} else {
		nodeFilters = append(nodeFilters, filter.Grpc())
	}

	// target中的查询参数作为md过滤器传入selector
	mdFilter := targetUrl.Query()
	if len(mdFilter) > 0 {
		nodeFilters = append(nodeFilters, filter.MetadataV2(mdFilter))
	}

	return []grpc.ClientOption{
		grpc.WithEndpoint(target),
		grpc.WithDiscovery(f.discovery),
		grpc.WithNodeFilter(nodeFilters...),
	}, nil
}

func (f *Factory) useHttpDiscovery(target string, key clientKey) ([]http.ClientOption, error) {
	if f.discovery == nil {
		return nil, ErrDiscoveryNotInitialized
	}

	targetUrl, err := url.Parse(target)
	if err != nil {
		return nil, errors.Wrap(err, ErrParseTargetFailed.Error())
	}

	// 默认 env 和 grpc 协议的过滤器
	var nodeFilters = []selector.NodeFilter{
		filter.Env(),
	}
	if key.isSecurity() {
		nodeFilters = append(nodeFilters, filter.Https())
	} else {
		nodeFilters = append(nodeFilters, filter.Http())
	}

	// target中的查询参数作为md过滤器传入selector
	mdFilter := targetUrl.Query()
	if len(mdFilter) > 0 {
		nodeFilters = append(nodeFilters, filter.MetadataV2(mdFilter))
	}

	return []http.ClientOption{
		http.WithEndpoint(target),
		http.WithDiscovery(f.discovery),
		http.WithNodeFilter(nodeFilters...),
	}, nil
}
