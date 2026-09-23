package client

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	deadlinemiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/deadline"
	loggingmiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/logging"
	metadatamiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metadata"
	metricsmiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/requestdebug"
	tracingmiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/tracing"
	foundationhttp "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	circuitbreakermiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client/internal/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// builder 保存创建客户端所需的不可变依赖。
type builder struct {
	// logger 客户端构造日志入口。
	logger log.Logger
	// tracing 客户端链路观测提供方。
	tracing tracing.Provider
	// metrics 客户端指标提供方。
	metrics metrics.Provider
	// discoveries 按名称解析服务发现实例的依赖。
	discoveries DiscoveryResolver
	// environment 构造时固定的应用环境，用于筛选服务发现节点。
	environment string
	// hostname 构造时固定的主机名，本地环境选址时优先同主机节点。
	hostname string
}

// newBuilder 保存客户端构造依赖，并固定此应用的环境和主机名。
func newBuilder(
	logger log.Logger,
	appInfo appinfo.AppInfo,
	tracingProvider tracing.Provider,
	metricsProvider metrics.Provider,
	discoveryProvider DiscoveryResolver,
) *builder {
	metadata := appInfo.Metadata()
	return &builder{
		logger:      logger,
		tracing:     tracingProvider,
		metrics:     metricsProvider,
		discoveries: discoveryProvider,
		environment: metadata[appinfo.MetadataEnvironment],
		hostname:    metadata[appinfo.MetadataHostname],
	}
}

// build 按客户端协议创建一个可由 Factory 接管关闭责任的传输客户端。
func (b *builder) build(ctx context.Context, spec clientSpec) (clientResult, error) {
	var (
		result clientResult
		err    error
	)

	switch spec.protocol {
	case config_pb.Protocol_GRPC:
		var client *stdgrpc.ClientConn
		client, err = b.newGRPCClient(ctx, spec)
		if err == nil {
			result = clientResult{grpcClient: client, closeFn: client.Close}
		}
	case config_pb.Protocol_HTTP, config_pb.Protocol_HTTPS:
		var (
			client         *kratoshttp.Client
			closeTransport func()
		)
		client, closeTransport, err = b.newHTTPClient(ctx, spec)
		if err == nil {
			result = clientResult{
				httpClient: client,
				closeFn: func() error {
					closeErr := client.Close()
					if closeTransport != nil {
						closeTransport()
					}
					return closeErr
				},
			}
		}
	default:
		err = fmt.Errorf("%w: %s (%d)", ErrInvalidProtocol, spec.protocol, spec.protocol)
	}
	if err != nil {
		return clientResult{}, err
	}
	if err := result.validate(); err != nil {
		if closeErr := result.close(); closeErr != nil {
			return clientResult{}, fmt.Errorf("close invalid client result: %w", closeErr)
		}
		return clientResult{}, err
	}
	return result, nil
}

func (b *builder) newGRPCClient(ctx context.Context, spec clientSpec) (*stdgrpc.ClientConn, error) {
	opts := []kratosgrpc.ClientOption{kratosgrpc.WithEndpoint(spec.target)}
	if spec.useDiscovery() {
		discovery, resolveErr := b.resolveDiscovery(spec)
		if resolveErr != nil {
			return nil, fmt.Errorf(
				"configure gRPC discovery for client %q target %q: %w",
				spec.name,
				spec.target,
				resolveErr,
			)
		}
		filters, err := b.getNodeFilters(spec)
		if err != nil {
			return nil, err
		}
		opts = append(opts,
			kratosgrpc.WithDiscovery(discovery),
			kratosgrpc.WithNodeFilter(filters...),
		)
	}

	middlewares, err := b.newMiddleware(spec)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kratosgrpc.WithTimeout(0),
		kratosgrpc.WithMiddleware(middlewares...),
		kratosgrpc.WithUnaryInterceptor(grpcUnavailableContext(spec.name)),
		kratosgrpc.WithOptions(grpcReconnectOption(), stdgrpc.WithChainStreamInterceptor(requestdebug.StreamClient(spec.middleware.GetRequestDebug()))),
	)
	return kratosgrpc.DialInsecure(ctx, opts...)
}

// grpcUnavailableContext 在实际 RPC 失败时补上具名连接，避免底层拨号错误只留下节点 IP。
// 带结构化详情的业务状态原样返回；%w 保留连接失败时的 gRPC 状态码和错误链。
func grpcUnavailableContext(name string) stdgrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			return nil
		}
		grpcStatus, ok := status.FromError(err)
		if !ok || grpcStatus.Code() != codes.Unavailable || len(grpcStatus.Details()) > 0 {
			return err
		}
		return fmt.Errorf("rpc client %q method %q: %w", name, method, err)
	}
}

func (b *builder) newHTTPClient(ctx context.Context, spec clientSpec) (*kratoshttp.Client, func(), error) {
	opts := []kratoshttp.ClientOption{kratoshttp.WithEndpoint(spec.target)}
	lifetime, cancelRequests := context.WithCancel(ctx)
	transport, tlsConfig, closeTransport := newHTTPClientTransport(lifetime, spec.protocol == config_pb.Protocol_HTTPS)
	releaseTransport := func() {
		cancelRequests()
		if closeTransport != nil {
			closeTransport()
		}
	}
	opts = append(opts, kratoshttp.WithTransport(withRequestLifetime(lifetime, transport)))
	keepTransport := false
	defer func() {
		if !keepTransport {
			releaseTransport()
		}
	}()
	if spec.protocol == config_pb.Protocol_HTTPS {
		opts = append(opts, kratoshttp.WithTLSConfig(tlsConfig))
	}
	if spec.useDiscovery() {
		discovery, resolveErr := b.resolveDiscovery(spec)
		if resolveErr != nil {
			return nil, nil, fmt.Errorf(
				"configure HTTP discovery for client %q target %q: %w",
				spec.name,
				spec.target,
				resolveErr,
			)
		}
		filters, err := b.getNodeFilters(spec)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts,
			kratoshttp.WithDiscovery(discovery),
			kratoshttp.WithNodeFilter(filters...),
		)
	}

	middlewares, err := b.newMiddleware(spec)
	if err != nil {
		return nil, nil, err
	}
	opts = append(opts,
		kratoshttp.WithTimeout(0),
		kratoshttp.WithMiddleware(middlewares...),
		kratoshttp.WithErrorDecoder(foundationhttp.Decoder()),
	)
	client, err := kratoshttp.NewClient(ctx, opts...)
	if err != nil {
		return nil, nil, err
	}
	keepTransport = true
	return client, releaseTransport, nil
}

func (b *builder) newMiddleware(spec clientSpec) ([]middleware.Middleware, error) {
	deadlineStore, err := deadline.NewStore(spec.middleware.GetDeadline())
	if err != nil {
		return nil, err
	}
	metricsClient, err := metricsmiddleware.Client(b.metrics, spec.middleware.GetMetrics())
	if err != nil {
		return nil, fmt.Errorf("create client metrics middleware: %w", err)
	}

	middlewares := []middleware.Middleware{
		deadlinemiddleware.Client(deadlineStore),
		requestdebug.Client(spec.middleware.GetRequestDebug()),
		tracingmiddleware.Client(b.tracing, spec.middleware.GetTracing()),
		metadatamiddleware.Client(spec.middleware.GetMetadata()),
		metricsClient,
		loggingmiddleware.Client(b.logger, spec.middleware.GetLogging()),
		circuitbreakermiddleware.Client(spec.middleware.GetCircuitBreaker()),
	}
	filtered := middlewares[:0]
	for _, item := range middlewares {
		if item != nil {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}
