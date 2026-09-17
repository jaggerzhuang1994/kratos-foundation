package app

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
)

// App 持有 Kratos 应用及启动、停止所需的状态；组装阶段由 bootstrap 管理。
type App struct {
	// App 提供底层 Kratos 应用生命周期入口。
	*kratos.App
	// stop 执行幂等停止，并共享第一次停止的结果。
	stop func() error

	// serversMu 保护 Start 返回与 Stop 完成的计数和完成通知。
	serversMu sync.Mutex
	// remaining 记录尚未返回的 Start 与尚未完成的 Stop 数量。
	remaining int
	// serversDone 全部运行时启动调用返回且停止完成后关闭。
	serversDone chan struct{}

	// parent 保存父上下文，用于将取消转换为统一停机。
	parent context.Context
	// parentDone 通知父上下文监听运行时退出。
	parentDone chan struct{}
	// parentStopOnce 保证父上下文监听退出信号只关闭一次。
	parentStopOnce sync.Once

	// beforeStart 保存启动前钩子。
	beforeStart []HookFunc
	// afterStart 保存启动后钩子。
	afterStart []HookFunc
	// beforeStop 保存停止前钩子。
	beforeStop []HookFunc
	// afterStop 保存停止后钩子。
	afterStop []HookFunc
	// stopPolicy 提供可热更新的停机预算，本次停机开始后冻结使用。
	stopPolicy *StopPolicy

	// ready 原子记录启动后钩子已全部成功；就绪判断还须排除 stopping。
	ready atomic.Bool
	// stopping 原子标记已请求停止。
	stopping atomic.Bool
	// stopOnce 保证停机状态与预算只初始化一次。
	stopOnce sync.Once
	// stopTime 保存本次停机冻结的预算，读取前经 stopOnce 同步。
	stopTime time.Duration

	// contextMu 保护停机上下文的读写。
	contextMu sync.RWMutex
	// stopCtx 保存停止前回调上下文，受 contextMu 保护；最终清理只继承其值。
	stopCtx context.Context

	// failureMu 保护生命周期失败结果。
	failureMu sync.Mutex
	// failureErr 保存首个需要向调用方返回的生命周期失败。
	failureErr error

	// beforeStopOnce 保证停止前钩子仅执行一次。
	beforeStopOnce sync.Once
	// beforeStopErr 保存停止前钩子的稳定结果。
	beforeStopErr error
	// afterStopOnce 保证停止后钩子仅执行一次。
	afterStopOnce sync.Once
	// afterStopErr 汇总首个故障、前后停止钩子及最终错误，完成 afterStopOnce 后读取。
	afterStopErr error
	// finalError 读取服务注销等附加停机错误。
	finalError func() error
}

// NewApp 冻结 Spec 并构造应用；serviceRegistrar 为 nil 时关闭服务注册。
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
	if config == nil {
		return nil, errors.New("app config is nil")
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
	stop := onceStop(func() error {
		application.requestStop()
		return application.App.Stop()
	})
	application.stop = stop
	servers := make([]transport.Server, len(snapshot.runtimes))
	for index, runtime := range snapshot.runtimes {
		servers[index] = runtime
	}
	if snapshot.context.Done() != nil {
		servers = append(servers, &serverCallbacks{start: application.waitParent, stop: application.stopParent})
	}
	application.initServers(len(servers))
	for index, runtime := range servers {
		servers[index] = application.wrapServer(runtime)
	}

	var registrar *supervisedRegistrar
	if serviceRegistrar != nil {
		registrar = newSupervisedRegistrar(
			serviceRegistrar,
			application,
			config.GetRegistrarTimeout().AsDuration(),
		)
		application.setFinalError(registrar.finalError)
	}

	options := []kratos.Option{
		kratos.ID(snapshot.appInfo.ID()),
		kratos.Name(snapshot.appInfo.Name()),
		kratos.Version(snapshot.appInfo.Version()),
		kratos.Metadata(metadata),
		// App 将父 Context 取消转为统一 Stop；直接传递取消状态会
		// 绕过 BeforeStop、服务注销和统一错误收敛。
		kratos.Context(context.WithoutCancel(snapshot.context)),
		kratos.Logger(snapshot.logger),
		kratos.Server(servers...),
		// 每个受监督运行时使用冻结后的 StopPolicy 截止时间，关闭 Kratos 的第二层
		// 超时可以避免嵌套截止时间让后注册的运行时拿不到完整预算。
		kratos.StopTimeout(0),
		kratos.BeforeStart(func(hookCtx context.Context) error {
			if err := snapshot.context.Err(); err != nil {
				return application.stopBeforeStart(hookCtx, err)
			}
			if err := application.runBeforeStart(hookCtx); err != nil {
				return application.stopBeforeStart(hookCtx, err)
			}
			if err := snapshot.context.Err(); err != nil {
				return application.stopBeforeStart(hookCtx, err)
			}
			if application.isStopping() {
				return application.stopBeforeStart(hookCtx, errAppStopping)
			}
			return nil
		}),
		kratos.AfterStart(func(hookCtx context.Context) error {
			if err := application.runAfterStart(hookCtx); err != nil {
				return application.stopAfterFailure(hookCtx, err)
			}
			// 钩子返回之后仍可能收到停止请求（例如此刻父 Context 被取消）。再查一次
			// 可以避免启动成功覆盖并发到达的停止请求。
			if application.isStopping() {
				return application.stopAfterFailure(hookCtx, errAppStopping)
			}
			return nil
		}),
		kratos.BeforeStop(application.runBeforeStop),
		kratos.AfterStop(application.runAfterStop),
	}
	signals := snapshot.signals
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}
	}
	options = append(options, kratos.Signal(signals...))
	if len(config.GetEndpoints()) > 0 || len(snapshot.endpoints) > 0 {
		endpoints := make([]*url.URL, 0, len(config.GetEndpoints())+len(snapshot.endpoints))
		for _, endpoint := range config.GetEndpoints() {
			endpoints = append(endpoints, &url.URL{
				Scheme: endpoint.GetScheme(),
				Host:   endpoint.GetHost(),
			})
		}
		endpoints = append(endpoints, snapshot.endpoints...)
		options = append(options, kratos.Endpoint(endpoints...))
	}
	if registrar != nil {
		options = append(
			options,
			kratos.Registrar(&continuingRegistrar{registrar: registrar}),
			kratos.RegistrarTimeout(config.GetRegistrarTimeout().AsDuration()),
		)
	}

	application.App = newKratosApplication(options...)
	spec.application.Store(application)
	return application, nil
}

// newApp 从冻结快照初始化应用状态，并保留可热更新的停机策略引用。
func newApp(snapshot appSnapshot, stopPolicy *StopPolicy) *App {
	return &App{
		parent:      snapshot.context,
		parentDone:  make(chan struct{}),
		beforeStart: append([]HookFunc(nil), snapshot.beforeStart...),
		afterStart:  append([]HookFunc(nil), snapshot.afterStart...),
		beforeStop:  append([]HookFunc(nil), snapshot.beforeStop...),
		afterStop:   append([]HookFunc(nil), snapshot.afterStop...),
		stopPolicy:  stopPolicy,
	}
}

// newKratosApplication 禁止 kratos.New 重写全局 Logger。框架日志统一通过
// Foundation 初始化时安装的稳定代理输出，实际绑定由 Log Bootstrap 管理。
func newKratosApplication(options ...kratos.Option) *kratos.App {
	return kratos.New(append(options, kratos.Logger(nil))...)
}

// onceStop 只执行一次底层停止操作，并让并发调用者等待和共享同一个稳定结果。
func onceStop(stop func() error) func() error {
	var once sync.Once
	done := make(chan struct{})
	var stopErr error
	return func() error {
		once.Do(func() {
			defer close(done)
			stopErr = stop()
		})
		<-done
		return stopErr
	}
}

// ErrStopRequested 表示运行时主动请求应用正常停止。
var ErrStopRequested = errors.New("application stop requested")

// ErrSpecFrozen 表示应用已经冻结，不能再登记贡献。
var ErrSpecFrozen = errors.New("app spec is frozen")

// Runtime 定义可启动和停止的应用运行时。
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
