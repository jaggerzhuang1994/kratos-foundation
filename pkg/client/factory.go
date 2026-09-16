package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Factory 按连接名称共享并租用 HTTP 与 gRPC 客户端。
//
// 每次成功 AcquireClient 都必须在本次调用结束时执行其幂等 release；遗漏 release
// 会在 cleanup 预算耗尽后被强制关闭。
type Factory interface {
	// AcquireClient 返回连接名称对应的客户端和幂等 release。成功返回后，调用方必须
	// 在本次调用结束时执行 release；遗漏 release 会在 cleanup 预算耗尽后被强制关闭。
	AcquireClient(ctx context.Context, name string) (*kratoshttp.Client, *stdgrpc.ClientConn, func(), error)
}

type clientBuilder interface {
	validateConfig(*config_pb.Client) error
	build(context.Context, clientSpec) (clientResult, error)
}

type clientSlot struct {
	name    string
	current *clientVersion
	build   *buildCall
}

type clientVersion struct {
	revision     uint64
	spec         clientSpec
	client       clientResult
	references   int
	retired      bool
	retireReason retireReason
}

type buildCall struct {
	version *clientVersion
	done    chan struct{}
	cancel  context.CancelFunc
	err     error
}

type retireReason string

const (
	retireConfigUpdated retireReason = "config_updated"
	retireConfigRemoved retireReason = "config_removed"
	retireStaleBuild    retireReason = "stale_build"
	retireFactoryClosed retireReason = "factory_closed"
)

type factory struct {
	logger         foundationlog.Logger
	builder        clientBuilder
	mu             sync.Mutex
	config         *config_pb.Client
	slots          map[string]*clientSlot
	closed         bool
	activities     int
	drained        chan struct{}
	leases         map[*clientVersion]string
	cleanupTimeout time.Duration
}

var _ Factory = (*factory)(nil)

// NewFactory 使用具名发现实例创建客户端工厂。
// cleanup 必须先于 DiscoveryResolver 的资源释放执行。
func NewFactory(
	manager config.Manager,
	logger foundationlog.Logger,
	info appinfo.AppInfo,
	tracingProvider tracing.Provider,
	metricsProvider metrics.Provider,
	discoveries DiscoveryResolver,
) (Factory, func(), error) {
	initial, moduleLogger, err := loadFactoryConfig(manager, logger)
	if err != nil {
		return nil, nil, err
	}
	b := newBuilder(moduleLogger, info, tracingProvider, metricsProvider, discoveries)
	return newConfiguredFactory(manager, b, moduleLogger, initial)
}

func loadFactoryConfig(manager config.Manager, logger foundationlog.Logger) (*config_pb.Client, foundationlog.Logger, error) {
	initial := new(config_pb.Client)
	if err := manager.Load("client", initial, new(config_pb.Client)); err != nil {
		return nil, nil, fmt.Errorf("load client config: %w", err)
	}
	logger = logger.WithModule("client")
	return initial, logger, nil
}

func newConfiguredFactory(
	manager config.Manager,
	builder clientBuilder,
	logger foundationlog.Logger,
	initial *config_pb.Client,
) (Factory, func(), error) {
	if err := builder.validateConfig(initial); err != nil {
		return nil, nil, err
	}
	timeout, err := clientCleanupTimeout(initial)
	if err != nil {
		return nil, nil, err
	}

	f := &factory{
		logger:         logger,
		builder:        builder,
		config:         proto.CloneOf(initial),
		slots:          make(map[string]*clientSlot, len(initial.GetClients())),
		leases:         make(map[*clientVersion]string),
		cleanupTimeout: timeout,
	}
	for name, option := range initial.GetClients() {
		f.slots[name] = &clientSlot{
			name: name,
			current: &clientVersion{
				revision: 1,
				spec:     newClientSpec(name, option, initial),
			},
		}
	}

	observer := func(_ string, value any, deliveryErr error) {
		if err := f.beginActivity(); err != nil {
			return
		}
		defer f.endActivity()

		if deliveryErr != nil {
			if !errors.Is(deliveryErr, ErrFactoryClosed) {
				f.logger.With("error", deliveryErr).Error("client config update rejected")
			}
			return
		}
		next, ok := value.(*config_pb.Client)
		if !ok || next == nil {
			deliveryErr = fmt.Errorf("client config update has type %T", value)
		} else {
			deliveryErr = f.updateConfigActive(next)
		}
		if deliveryErr != nil && !errors.Is(deliveryErr, ErrFactoryClosed) {
			f.logger.With("error", deliveryErr).Error("client config update rejected")
		}
	}
	cancelSubscription, err := manager.Subscribe(
		"client",
		new(config_pb.Client),
		observer,
		new(config_pb.Client),
	)
	if err != nil {
		f.cleanup(cancelSubscription)()
		return nil, nil, fmt.Errorf("subscribe client config: %w", err)
	}
	return f, f.cleanup(cancelSubscription), nil
}

var (
	// ErrInvalidClientName 表示调用上下文未包含连接名称。
	ErrInvalidClientName = errors.New("client connection name is missing")
	// ErrFactoryClosed 表示客户端工厂已开始关闭。
	ErrFactoryClosed = errors.New("client factory is closed")
	// ErrDiscoveryNotInitialized 表示服务发现目标缺少发现实现。
	ErrDiscoveryNotInitialized = errors.New("discovery not initialized")
	// ErrInvalidProtocol 表示客户端配置使用了不支持的协议。
	ErrInvalidProtocol = errors.New("invalid client protocol")
	// ErrInvalidBuildResult 表示构建结果没有恰好包含一个协议客户端。
	ErrInvalidBuildResult = errors.New("invalid client build result")
)
