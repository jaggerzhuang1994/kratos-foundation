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
	"github.com/go-kratos/kratos/v2/registry"
)

// Spec 保存 Bootstrap 阶段的可变组装状态。
type Spec struct {
	application atomic.Pointer[App]
	mu          sync.Mutex
	frozen      bool

	runtimes   []Runtime
	decorators []ContextDecorator

	appInfo   AppInfo
	logger    kratoslog.Logger
	registrar registry.Registrar
	metadata  map[string]string
	endpoints []*url.URL
	signals   []os.Signal

	beforeStart []HookFunc
	afterStart  []HookFunc
	beforeStop  []HookFunc
	afterStop   []HookFunc
}

// appSnapshot 是冻结 Spec 后供应用生命周期消费的不可变组装输入。
type appSnapshot struct {
	context context.Context

	appInfo   AppInfo
	logger    kratoslog.Logger
	registrar registry.Registrar
	metadata  map[string]string
	endpoints []*url.URL
	signals   []os.Signal
	runtimes  []Runtime

	beforeStart []HookFunc
	afterStart  []HookFunc
	beforeStop  []HookFunc
	afterStop   []HookFunc
}

func NewSpec() *Spec {
	return &Spec{
		metadata: make(map[string]string),
	}
}

// RegisterRuntime 按登记顺序追加 Runtime，不对实例去重。
func (s *Spec) RegisterRuntime(runtime Runtime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	s.runtimes = append(s.runtimes, runtime)
	return nil
}

func (s *Spec) RegisterAppInfo(info AppInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	if s.appInfo != nil {
		return fmt.Errorf("app info is already registered")
	}
	s.appInfo = info
	return nil
}

func (s *Spec) RegisterLogger(logger kratoslog.Logger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	if s.logger != nil {
		return fmt.Errorf("app logger is already registered")
	}
	s.logger = logger
	return nil
}

func (s *Spec) RegisterRegistrar(registrar registry.Registrar) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	if s.registrar != nil {
		return fmt.Errorf("app registrar is already registered")
	}
	s.registrar = registrar
	return nil
}

// AddContext 按登记顺序追加上下文装饰函数；重复登记的函数会重复执行。
func (s *Spec) AddContext(decorate ContextDecorator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	s.decorators = append(s.decorators, decorate)
	return nil
}

func (s *Spec) AddMetadata(metadata map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	maps.Copy(s.metadata, metadata)
	return nil
}

func (s *Spec) AddEndpoints(endpoints ...*url.URL) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	s.endpoints = append(s.endpoints, endpoints...)
	return nil
}

func (s *Spec) AddSignals(signals ...os.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	s.signals = append(s.signals, signals...)
	return nil
}

func (s *Spec) BeforeStart(hooks ...HookFunc) error {
	return s.addHooks(&s.beforeStart, hooks)
}

func (s *Spec) AfterStart(hooks ...HookFunc) error {
	return s.addHooks(&s.afterStart, hooks)
}

func (s *Spec) BeforeStop(hooks ...HookFunc) error {
	return s.addHooks(&s.beforeStop, hooks)
}

func (s *Spec) AfterStop(hooks ...HookFunc) error {
	return s.addHooks(&s.afterStop, hooks)
}

func (s *Spec) addHooks(destination *[]HookFunc, hooks []HookFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutable(); err != nil {
		return err
	}
	*destination = append(*destination, hooks...)
	return nil
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
		registrar:   s.registrar,
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

func (s *Spec) checkMutable() error {
	if s.frozen {
		return ErrSpecFrozen
	}
	return nil
}

// Ready 在全部启动后钩子成功且尚未请求停机时返回 true，可并发用于就绪探针。
func (s *Spec) Ready() bool {
	application := s.application.Load()
	return application != nil && application.ready.Load() && !application.isStopping()
}
