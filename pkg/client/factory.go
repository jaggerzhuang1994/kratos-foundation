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
	// name 具名客户端的连接名称。
	name string
	// current 当前配置版本，由 factory.mu 保护。
	current *clientVersion
	// build 该连接正在进行的共享构建；nil 表示无构建。
	build *buildCall
}

type clientVersion struct {
	// revision 连接配置版本号，用于识别过期构建。
	revision uint64
	// spec 本版本归一化的只读配置。
	spec clientSpec
	// client 本版本持有的协议客户端与释放函数。
	client clientResult
	// references 尚未释放的调用租约数，由 factory.mu 保护。
	references int
	// retired 是否停止接收新租约，等待旧租约归还。
	retired bool
	// retireReason 退役原因，用于释放日志。
	retireReason retireReason
}

type buildCall struct {
	// version 本次构建绑定的配置版本。
	version *clientVersion
	// done 构建结束时关闭，唤醒共享等待者。
	done chan struct{}
	// cancel 取消尚未完成的构建；成功后转交客户端释放函数。
	cancel context.CancelFunc
	// err 构建失败结果，完成后由等待者读取。
	err error
}

type retireReason string

const (
	retireConfigUpdated retireReason = "config_updated"
	retireConfigRemoved retireReason = "config_removed"
	retireStaleBuild    retireReason = "stale_build"
	retireFactoryClosed retireReason = "factory_closed"
)

type factory struct {
	// logger 客户端构建、更新与释放日志入口。
	logger foundationlog.Logger
	// builder 按配置构造协议客户端的实现。
	builder clientBuilder
	// mu 保护配置、连接版本、租约及关闭状态。
	mu sync.Mutex
	// config 最近接受的独立配置副本；cleanupTimeout 仍使用构造时值。
	config *config_pb.Client
	// slots 按连接名称保存当前版本与构建状态。
	slots map[string]*clientSlot
	// closed 是否已进入关闭阶段，阻止新租约和活动。
	closed bool
	// activities 未结束的构建、租约与配置更新活动数。
	activities int
	// drained 关闭期间活动归零后关闭的等待信号。
	drained chan struct{}
	// leases 持有未归还租约的版本及连接名，用于停机清理。
	leases map[*clientVersion]string
	// cleanupTimeout 构造时冻结的租约等待预算，默认 30 秒；超时后强制回收，不限制底层 Close 耗时。
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
