package app

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"sync"
	"sync/atomic"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Spec 收集 Bootstrap 阶段的应用声明；构造应用后冻结，公开登记调用会 panic(ErrSpecFrozen)。
type Spec struct {
	// application 原子发布已构造应用，供就绪探针读取。
	application atomic.Pointer[App]
	// mu 保护组装声明及冻结状态。
	mu sync.Mutex
	// frozen 记录声明是否已冻结。
	frozen bool

	// runtimes 保存按登记顺序排列的运行时引用；资源 cleanup 仍由组装层管理。
	runtimes []Runtime
	// decorators 按登记顺序装饰应用上下文。
	decorators []ContextDecorator

	// appInfo 保存应用身份引用。
	appInfo AppInfo
	// logger 保存供 Kratos 使用的日志视图。
	logger kratoslog.Logger
	// metadata 保存应用元数据快照，同名键以后登记值为准。
	metadata map[string]string
	// endpoints 保存对外端点；URL 对象共享，调用方须保持只读。
	endpoints []*url.URL
	// signals 保存触发应用停止的系统信号。
	signals []os.Signal

	// beforeStart 保存启动前执行的钩子。
	beforeStart []HookFunc
	// afterStart 保存启动后执行的钩子。
	afterStart []HookFunc
	// beforeStop 保存停止前执行的钩子。
	beforeStop []HookFunc
	// afterStop 保存停止后执行的钩子。
	afterStop []HookFunc
}

// appSnapshot 是冻结 Spec 后供应用生命周期消费的不可变组装输入。
type appSnapshot struct {
	// context 保存装饰完成的应用父上下文。
	context context.Context

	// appInfo 保存应用身份引用。
	appInfo AppInfo
	// logger 保存供 Kratos 使用的日志视图。
	logger kratoslog.Logger
	// metadata 保存应用元数据快照，同名键以后登记值为准。
	metadata map[string]string
	// endpoints 保存对外端点；URL 对象共享，调用方须保持只读。
	endpoints []*url.URL
	// signals 保存触发应用停止的系统信号。
	signals []os.Signal
	// runtimes 保存按登记顺序排列的运行时引用；资源 cleanup 仍由组装层管理。
	runtimes []Runtime

	// beforeStart 保存启动前执行的钩子。
	beforeStart []HookFunc
	// afterStart 保存启动后执行的钩子。
	afterStart []HookFunc
	// beforeStop 保存停止前执行的钩子。
	beforeStop []HookFunc
	// afterStop 保存停止后执行的钩子。
	afterStop []HookFunc
}

// NewSpec 创建可登记的应用组装状态。
func NewSpec() *Spec {
	return &Spec{
		metadata: make(map[string]string),
	}
}

// RegisterRuntime 按登记顺序追加 Runtime，不对实例去重。
func (s *Spec) RegisterRuntime(runtime Runtime) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	s.runtimes = append(s.runtimes, runtime)
}

// RegisterAppInfo 登记唯一的应用身份；重复登记非 nil 身份会 panic。
func (s *Spec) RegisterAppInfo(info AppInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	if s.appInfo != nil {
		panic("app info is already registered")
	}
	s.appInfo = info
}

// RegisterLogger 登记仅供 Kratos App 使用的带 module=kratos 的派生 Logger；重复登记会 panic。
// 派生视图借用原输出，不修改输入 Logger 或全局绑定。
func (s *Spec) RegisterLogger(logger kratoslog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	if s.logger != nil {
		panic("app logger is already registered")
	}
	s.logger = logger
	// nil 仍表示未登记，由 NewApp 保持原有缺失依赖错误。
	if logger != nil {
		if base, ok := logger.(foundationlog.Logger); ok {
			s.logger = base.WithModule("kratos")
		} else {
			s.logger = kratoslog.With(logger, "module", "kratos")
		}
	}
}

// AddContext 按登记顺序追加上下文装饰函数；重复登记的函数会重复执行。
func (s *Spec) AddContext(decorate ContextDecorator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	s.decorators = append(s.decorators, decorate)
}

// AddMetadata 复制并合并应用元数据，同名键以后登记的值为准。
func (s *Spec) AddMetadata(metadata map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	maps.Copy(s.metadata, metadata)
}

// AddEndpoints 追加对外端点，调用方须保持 URL 对象只读。
func (s *Spec) AddEndpoints(endpoints ...*url.URL) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	s.endpoints = append(s.endpoints, endpoints...)
}

// AddSignals 追加触发应用停止的信号。
func (s *Spec) AddSignals(signals ...os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	s.signals = append(s.signals, signals...)
}

// BeforeStart 追加启动前钩子。
func (s *Spec) BeforeStart(hooks ...HookFunc) {
	s.addHooks(&s.beforeStart, hooks)
}

// AfterStart 追加启动后钩子。
func (s *Spec) AfterStart(hooks ...HookFunc) {
	s.addHooks(&s.afterStart, hooks)
}

// BeforeStop 追加停止前钩子。
func (s *Spec) BeforeStop(hooks ...HookFunc) {
	s.addHooks(&s.beforeStop, hooks)
}

// AfterStop 追加停止后钩子。
func (s *Spec) AfterStop(hooks ...HookFunc) {
	s.addHooks(&s.afterStop, hooks)
}

func (s *Spec) addHooks(destination *[]HookFunc, hooks []HookFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkMutable()
	*destination = append(*destination, hooks...)
}

// freeze 关闭后续注册，并在锁外执行所有外部贡献。
func (s *Spec) freeze(base context.Context) (appSnapshot, error) {
	if base == nil {
		return appSnapshot{}, fmt.Errorf("base context is nil")
	}

	s.mu.Lock()
	if s.frozen {
		s.mu.Unlock()
		return appSnapshot{}, ErrSpecFrozen
	}
	s.frozen = true
	snapshot := appSnapshot{
		appInfo:     s.appInfo,
		logger:      s.logger,
		metadata:    maps.Clone(s.metadata),
		endpoints:   append([]*url.URL(nil), s.endpoints...),
		signals:     append([]os.Signal(nil), s.signals...),
		beforeStart: append([]HookFunc(nil), s.beforeStart...),
		afterStart:  append([]HookFunc(nil), s.afterStart...),
		beforeStop:  append([]HookFunc(nil), s.beforeStop...),
		afterStop:   append([]HookFunc(nil), s.afterStop...),
	}
	snapshot.runtimes = append([]Runtime(nil), s.runtimes...)
	decorators := append([]ContextDecorator(nil), s.decorators...)
	s.mu.Unlock()

	ctx := base
	for index, decorate := range decorators {
		next := decorate(ctx)
		if next == nil {
			return appSnapshot{}, fmt.Errorf("context contribution %d returned nil", index+1)
		}
		ctx = next
	}
	snapshot.context = ctx
	metadata := make(map[string]string, len(snapshot.metadata))
	if snapshot.appInfo != nil {
		maps.Copy(metadata, snapshot.appInfo.Metadata())
	}
	maps.Copy(metadata, snapshot.metadata)
	snapshot.metadata = metadata
	return snapshot, nil
}

// checkMutable 必须持有 mu 调用；冻结后写入属于组装错误，panic 时由调用方 defer 解锁。
func (s *Spec) checkMutable() {
	if s.frozen {
		panic(ErrSpecFrozen)
	}
}

// Ready 在全部启动后钩子成功且尚未请求停机时返回 true，可并发用于就绪探针。
func (s *Spec) Ready() bool {
	application := s.application.Load()
	return application != nil && application.ready.Load() && !application.isStopping()
}
