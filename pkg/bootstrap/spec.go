package bootstrap

import (
	"fmt"
	"net/url"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

// LocalConfigPath 是本地配置路径，用于区分 Wire 中的其他字符串依赖。
type LocalConfigPath string

// RemoteConfigPathsProvider 根据配置名称和环境生成有序远程配置路径，由业务提供。
// 配置源组装方按返回顺序加载路径，具体路径语法由对应配置源约束。
type RemoteConfigPathsProvider func(name, environment string) []string

// Spec 按领域收集应用声明。使用 NewSpec 构造，仅在组装前串行修改。
type Spec struct {
	configuration      []config.SourceLoader
	configurationBuilt bool
	application        *app.Spec
	server             *server.Spec
	serverBuilt        bool
	http, grpc         bool
	jobs               *job.Spec
	jobsBuilt          bool
	runtimes           []app.Runtime
	assembled          bool
}

// NewSpec 创建应用组件及生命周期声明。
func NewSpec() *Spec {
	return &Spec{application: app.NewSpec()}
}

// ApplicationSpec 向 Foundation provider 暴露同一应用状态，不应同时提供 app.NewSpec。
func ApplicationSpec(spec *Spec) *app.Spec { return spec.application }

// Configuration 按顺序声明额外配置源；声明阶段不执行 I/O。
// 必须在提供 Spec 的构造函数中调用，不能放进依赖 Manager 的业务 Boot。
func (s *Spec) Configuration(loaders ...config.SourceLoader) error {
	if s.configurationBuilt {
		return fmt.Errorf("configuration is already assembled")
	}
	for _, loader := range loaders {
		if loader == nil {
			return fmt.Errorf("config source loader is nil")
		}
	}
	s.configuration = append(s.configuration, loaders...)
	return nil
}

// NewConfigManager 执行 Configuration 阶段，再向 Wire 提供完整的应用配置。
// 配置声明串行执行且仅能消费一次；失败后丢弃 Spec。cleanup 归组装层所有。
func NewConfigManager(spec *Spec) (config.Manager, func(), error) {
	if spec.configurationBuilt {
		return nil, nil, fmt.Errorf("configuration is already assembled")
	}
	spec.configurationBuilt = true
	var sources config.Sources
	for index, loader := range spec.configuration {
		next, err := loader()
		if err != nil {
			return nil, nil, fmt.Errorf("create config source %d: %w", index, err)
		}
		sources = append(sources, next...)
	}
	return config.NewManager(sources)
}

// Http 选择 HTTP 并返回端点声明；默认遵循配置中的 disable。
func (s *Spec) Http() server.HTTPBuilder {
	s.http = true
	return s.serverSpec().HTTP()
}

// Grpc 选择 gRPC 并返回服务声明；未选择的协议不会创建。
func (s *Spec) Grpc() server.GRPCBuilder {
	s.grpc = true
	return s.serverSpec().GRPC()
}

func (s *Spec) serverSpec() *server.Spec {
	if s.server == nil {
		s.server = server.NewSpec()
	}
	return s.server
}

// Job 返回任务声明，支持 Cron、Once 和 Daemon。
func (s *Spec) Job() job.Builder {
	if s.jobs == nil {
		s.jobs = job.NewSpec()
	}
	return s.jobs
}

// RegisterRuntime 登记 Wire 构造的通用运行时，包括 queue.Worker、kafka.ConsumerRuntime 和自定义 worker。
// App 管理 Start/Stop，资源 cleanup 仍归构造该运行时的 provider 所有。
func (s *Spec) RegisterRuntime(runtime app.Runtime) *Spec {
	s.runtimes = append(s.runtimes, runtime)
	return s
}

// AddContext 追加应用启动上下文的装饰函数。
func (s *Spec) AddContext(decorate app.ContextDecorator) error {
	return s.application.AddContext(decorate)
}

// AddMetadata 追加应用元数据。
func (s *Spec) AddMetadata(metadata map[string]string) error {
	return s.application.AddMetadata(metadata)
}

// AddEndpoints 追加对外公布的端点地址。
func (s *Spec) AddEndpoints(endpoints ...*url.URL) error {
	return s.application.AddEndpoints(endpoints...)
}

// AddSignals 指定触发应用退出的系统信号。
func (s *Spec) AddSignals(signals ...os.Signal) error {
	return s.application.AddSignals(signals...)
}

// BeforeStart 登记运行时启动前执行的业务钩子。
func (s *Spec) BeforeStart(hooks ...app.HookFunc) error {
	return s.application.BeforeStart(hooks...)
}

// AfterStart 登记所有运行时启动后执行的业务钩子。
func (s *Spec) AfterStart(hooks ...app.HookFunc) error {
	return s.application.AfterStart(hooks...)
}

// BeforeStop 登记运行时停止前执行的业务钩子。
func (s *Spec) BeforeStop(hooks ...app.HookFunc) error {
	return s.application.BeforeStop(hooks...)
}

// AfterStop 登记所有运行时停止后执行的业务钩子。
func (s *Spec) AfterStop(hooks ...app.HookFunc) error {
	return s.application.AfterStop(hooks...)
}
