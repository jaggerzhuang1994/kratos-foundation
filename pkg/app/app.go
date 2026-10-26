package app

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// App 管理应用的启动、就绪、停止与服务注册生命周期，并实现 kratos.AppInfo，
// 运行时与钩子的 Context 可通过 kratos.FromContext 读取应用身份。
type App struct {
	// id、name、version 是冻结时的应用身份。
	id, name, version string
	// metadata 是合并后的应用元数据，构造后只读。
	metadata map[string]string
	// logger 是 module=app 的生命周期日志视图，不依赖当前全局绑定。
	logger kratoslog.Logger
	// parent 是装饰完成的父 Context；取消时转为统一停止。
	parent context.Context
	// base 保留父 Context 的值与应用身份但不传播取消，供 Runtime 启停、停止钩子与清理使用。
	base context.Context
	// runCtx 派生自 base，在首次停止请求时取消，供启动钩子和服务注册使用。
	runCtx    context.Context
	cancelRun context.CancelFunc

	// runtimes 按登记顺序保存运行时；启动前回滚按逆序执行。
	runtimes []Runtime
	// beforeStart、afterStart、beforeStop、afterStop 是冻结时复制的生命周期钩子。
	beforeStart, afterStart, beforeStop, afterStop []HookFunc
	// signals 是触发停止的系统信号。
	signals []os.Signal
	// endpoints 是显式端点（配置在前、Spec 在后）；为空时从 Runtime 的 Endpoint 解析。
	endpoints []*url.URL
	// registrar 为 nil 表示不注册服务。
	registrar registry.Registrar
	// registrarTimeout 约束单次注册和注销。
	registrarTimeout time.Duration
	// stopPolicy 提供可热更新的停机预算，首次停止请求时冻结。
	stopPolicy *StopPolicy

	// running 保证 Run 只执行一次。
	running atomic.Bool
	// ready 在启动后钩子全部成功时置位；就绪判断还须排除 stopping。
	ready atomic.Bool
	// readySignal 与 Spec 共享，首次就绪时关闭。
	readySignal chan struct{}
	// stopping 原子标记已请求停止，先于 stoppingSignal 发布。
	stopping atomic.Bool
	// stoppingSignal 与 Spec 共享，首次停止请求时关闭，同时唤醒 Run 进入停机。
	stoppingSignal chan struct{}
	// stopOnce 保证停机状态与预算只初始化一次；stopTime 在 stopOnce 内写入。
	stopOnce sync.Once
	stopTime time.Duration
	// beginStopOnce 保证停止前钩子与注销只执行一次，Stop 与 Run 共享结果。
	beginStopOnce sync.Once
	// beforeStopErr 在 beginStopOnce 内写入，完成后只读。
	beforeStopErr error

	// mu 保护注册状态、已解析端点和失败汇总；外部调用均在锁外执行。
	mu sync.Mutex
	// instance 是 Run 解析出的服务实例，Endpoint 读取其端点。
	instance *registry.ServiceInstance
	// registered、deregistered 保证注销最多执行一次，并覆盖注册期间到达的停止请求。
	registered, deregistered bool
	// deregisterErr 保存注销结果，参与最终错误汇总。
	deregisterErr error
	// failures 按发生顺序保存真实故障；仅取消的错误与停机噪声不计入。
	failures []error
}

// NewApp 冻结 Spec 并构造应用；Spec 禁用服务注册或 serviceRegistrar 为 nil 时不注册。
// 组件选择和组装阶段由调用方负责。
func NewApp(
	ctx context.Context,
	spec *Spec,
	config Config,
	stopPolicy *StopPolicy,
	serviceRegistrar registry.Registrar,
) (*App, error) {
	snapshot, err := spec.freeze(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot.appInfo == nil {
		return nil, errors.New("app info is not registered")
	}
	if snapshot.logger == nil {
		return nil, errors.New("app logger is not registered")
	}

	metadata := make(map[string]string, len(config.GetMetadata())+len(snapshot.metadata))
	maps.Copy(metadata, config.GetMetadata())
	// Bootstrap 贡献比静态配置更接近实际进程状态，因此同名元数据覆盖配置。
	maps.Copy(metadata, snapshot.metadata)

	application := newApp(snapshot, stopPolicy)
	application.id, application.name, application.version = snapshot.appInfo.ID(), snapshot.appInfo.Name(), snapshot.appInfo.Version()
	application.metadata = metadata
	environment := metadata["env"]
	if environment == "" {
		environment = env.AppEnv()
	}
	identity := []any{"service.id", application.id, "service.name", application.name, "service.version", application.version, "env", environment}
	if logger, ok := snapshot.logger.(foundationlog.Logger); ok {
		application.logger = logger.WithModule("app").With(identity...)
	} else {
		application.logger = kratoslog.With(snapshot.logger, append([]any{"module", "app"}, identity...)...)
	}
	for _, endpoint := range config.GetEndpoints() {
		application.endpoints = append(application.endpoints, &url.URL{Scheme: endpoint.GetScheme(), Host: endpoint.GetHost()})
	}
	application.endpoints = append(application.endpoints, snapshot.endpoints...)
	if snapshot.serviceRegistrationDisabled {
		application.logEvent(application.parent, kratoslog.LevelDebug, "app.registration.disabled", "service registration disabled")
	} else {
		application.registrar = serviceRegistrar
	}
	application.registrarTimeout = config.GetRegistrarTimeout().AsDuration()

	spec.application.Store(application)
	// 只输出身份和执行摘要，不展开任意 metadata、命令参数或配置值。
	application.logEvent(application.parent, kratoslog.LevelInfo, "app.assembled", "application assembled",
		"hostname", metadata["hostname"], "pid", os.Getpid(),
		"executable", filepath.Base(os.Args[0]), "go_version", runtime.Version(),
		"runtimes", len(snapshot.runtimes), "service_registration", application.registrar != nil)
	return application, nil
}

// newApp 从冻结快照初始化生命周期状态；身份、端点和注册器由 NewApp 补齐。
func newApp(snapshot appSnapshot, stopPolicy *StopPolicy) *App {
	signals := snapshot.signals
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}
	}
	application := &App{
		logger:         snapshot.logger,
		parent:         snapshot.context,
		runtimes:       snapshot.runtimes,
		beforeStart:    snapshot.beforeStart,
		afterStart:     snapshot.afterStart,
		beforeStop:     snapshot.beforeStop,
		afterStop:      snapshot.afterStop,
		signals:        signals,
		stopPolicy:     stopPolicy,
		readySignal:    snapshot.readySignal,
		stoppingSignal: snapshot.stoppingSignal,
	}
	application.base = kratos.NewContext(context.WithoutCancel(snapshot.context), application)
	application.runCtx, application.cancelRun = context.WithCancel(application.base)
	return application
}

// ID 返回应用实例 ID。
func (a *App) ID() string { return a.id }

// Name 返回服务名。
func (a *App) Name() string { return a.name }

// Version 返回服务版本。
func (a *App) Version() string { return a.version }

// Metadata 返回合并后应用元数据的副本。
func (a *App) Metadata() map[string]string { return maps.Clone(a.metadata) }

// Endpoint 返回 Run 解析出的服务端点；Run 解析端点之前返回 nil。
func (a *App) Endpoint() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.instance == nil {
		return nil
	}
	return slices.Clone(a.instance.Endpoints)
}

// logEvent 使用稳定事件名组织生命周期；msg 与其它领域日志保持同一可读格式。
func (a *App) logEvent(ctx context.Context, level kratoslog.Level, event, message string, fields ...any) {
	logger := a.logger
	if base, ok := logger.(foundationlog.Logger); ok {
		// 跳过这个适配方法，caller 指向实际的生命周期节点。
		logger = base.WithContext(ctx).WithCallerDepth(2)
	} else {
		logger = kratoslog.WithContext(ctx, logger)
	}
	keyvals := append([]any{"event", event}, fields...)
	kratoslog.NewHelper(logger).Log(level, append(keyvals, kratoslog.DefaultMessageKey, message)...)
}

// ErrStopRequested 表示运行时主动请求应用正常停止。
var ErrStopRequested = errors.New("application stop requested")

// ErrSpecFrozen 表示应用已经冻结，不能再登记贡献。
var ErrSpecFrozen = errors.New("app spec is frozen")

// Runtime 定义可启动和停止的应用运行时。
// 运行时可选实现 AbortStartup(context.Context) error，在 Endpoint 或 BeforeStart
// 失败时回收尚未启动的资源。该操作应幂等、响应取消，不能替代已启动运行时的 Stop。
type Runtime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// AppInfo 提供应用身份和静态元数据。
type AppInfo interface {
	ID() string
	Name() string
	Version() string
	Metadata() map[string]string
}

// HookFunc 在应用启动或停止阶段执行。
type HookFunc func(context.Context) error

// ContextDecorator 为应用及其运行时附加上下文。
type ContextDecorator func(context.Context) context.Context

var _ kratos.AppInfo = (*App)(nil)
