package client

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/observability"
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
	// HTTP 服务发现客户端不等待首个可用节点，节点尚未就绪时由实际请求返回发现错误。
	AcquireClient(ctx context.Context, name string) (*kratoshttp.Client, *stdgrpc.ClientConn, func(), error)
}

type clientBuilder interface {
	validateConfig(*config_pb.Client, observability.Defaults) error
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
	// retired 标记版本已停止授予新租约；在 factory.mu 内写入，快速路径无锁读取。
	retired atomic.Bool
	// leases 按分片保存未归还租约数，避免高并发 Acquire 争用同一缓存行。
	leases [leaseShards]leaseCounter
	// retireReason 退役原因，用于释放日志；在设置 retired 之前于 factory.mu 内写入。
	retireReason retireReason
}

// leaseShards 是每个版本的租约计数分片数，须为 2 的幂。
const leaseShards = 32

// leaseCounter 独占一个缓存行，避免相邻分片的伪共享。
type leaseCounter struct {
	n atomic.Int64
	_ [56]byte
}

// tryAcquire 在版本未退役时增加一个租约并返回所用分片。
//
// 与 retire 构成 Dekker 式握手：租约方先增计数再读 retired，退役方先写 retired 再汇总计数，
// 因此要么租约方看到退役并撤回，要么退役方看到这次计数而不会关闭版本。
// 撤回时返回 idle，表示调用方可能是最后一个计数者，须检查关闭。
func (v *clientVersion) tryAcquire() (shard int, ok, idle bool) {
	shard = int(rand.Uint32() & (leaseShards - 1))
	v.leases[shard].n.Add(1)
	if !v.retired.Load() {
		return shard, true, false
	}
	v.leases[shard].n.Add(-1)
	return 0, false, v.idle()
}

// releaseShard 归还一个租约，并报告版本是否已退役且没有剩余租约。
func (v *clientVersion) releaseShard(shard int) (idle bool) {
	v.leases[shard].n.Add(-1)
	return v.retired.Load() && v.idle()
}

// retire 必须持有 factory.mu 调用；标记版本退役并报告此刻是否已无租约。
// 无租约时由调用方立即关闭，否则由把计数归零的归还方关闭。
func (v *clientVersion) retire(reason retireReason) (idle bool) {
	v.retireReason = reason
	v.retired.Store(true)
	return v.idle()
}

// idle 汇总全部分片；持有中的租约使其所在分片始终为正，因此不会误判为零。
func (v *clientVersion) idle() bool {
	var total int64
	for index := range v.leases {
		total += v.leases[index].n.Load()
	}
	return total == 0
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
	// observabilityDefaults 是构造时固定的全局观测默认值；局部更新不重新读取全局配置。
	observabilityDefaults observability.Defaults
	// slots 按连接名称保存当前版本与构建状态。
	slots map[string]*clientSlot
	// closed 是否已进入关闭阶段，阻止新租约和活动。
	closed bool
	// published 原子发布“连接名 → 已构建且未退役的当前版本”的只读索引，供 Acquire 无锁读取；
	// 由持有 mu 的构建发布、配置更新和关闭重建，nil 表示无可用版本。
	published atomic.Pointer[map[string]*clientVersion]
	// activities 未结束的构建与配置更新活动数，由 mu 保护。
	activities int
	// draining 已退役但仍有租约的版本及连接名，由 mu 保护；最后一个租约归还时关闭并移除。
	draining map[*clientVersion]string
	// drained 关闭期间活动与租约都归零后关闭的等待信号；drainedClosed 防止重复关闭。
	drained       chan struct{}
	drainedClosed bool
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
	initial, defaults, moduleLogger, err := loadFactoryConfig(manager, logger)
	if err != nil {
		return nil, nil, err
	}
	b := newBuilder(moduleLogger, info, tracingProvider, metricsProvider, discoveries)
	return newConfiguredFactory(manager, b, moduleLogger, initial, defaults)
}

func loadFactoryConfig(manager config.Manager, logger foundationlog.Logger) (*config_pb.Client, observability.Defaults, foundationlog.Logger, error) {
	initial := new(config_pb.Client)
	if err := manager.Load("client", initial, new(config_pb.Client)); err != nil {
		return nil, observability.Defaults{}, nil, fmt.Errorf("load client config: %w", err)
	}
	// 全局默认值只读取一次；具名配置和动态回退共享同一份启动快照。
	defaults, err := observability.Load(manager)
	if err != nil {
		return nil, observability.Defaults{}, nil, fmt.Errorf("load client observability defaults: %w", err)
	}
	logger = logger.WithModule("client")
	return initial, defaults, logger, nil
}

func newConfiguredFactory(
	manager config.Manager,
	builder clientBuilder,
	logger foundationlog.Logger,
	initial *config_pb.Client,
	defaults observability.Defaults,
) (Factory, func(), error) {
	if err := builder.validateConfig(initial, defaults); err != nil {
		return nil, nil, err
	}
	timeout, err := clientCleanupTimeout(initial)
	if err != nil {
		return nil, nil, err
	}

	f := &factory{
		logger:                logger,
		builder:               builder,
		config:                proto.CloneOf(initial),
		observabilityDefaults: defaults,
		slots:                 make(map[string]*clientSlot, len(initial.GetClients())),
		draining:              make(map[*clientVersion]string),
		cleanupTimeout:        timeout,
	}
	for name, option := range initial.GetClients() {
		f.slots[name] = &clientSlot{
			name: name,
			current: &clientVersion{
				revision: 1,
				spec:     newClientSpec(name, option, initial, defaults),
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
				f.logger.With("event", "client.config.rejected", "error", deliveryErr).Warn("client config rejected; keeping previous")
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
			f.logger.With("event", "client.config.rejected", "error", deliveryErr).Warn("client config rejected; keeping previous")
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
	// ErrProtocolMismatch 表示 AcquireHTTP / AcquireGRPC 请求的协议与连接配置不符。
	ErrProtocolMismatch = errors.New("client protocol mismatch")
)
