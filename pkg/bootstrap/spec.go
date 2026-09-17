package bootstrap

import (
	"net/url"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

// Spec 按领域收集应用声明。使用 NewSpec 构造，仅支持串行组装，不支持并发调用。
// 业务在 Bootstrap provider 中声明蓝图，由 Wire 保证先声明后构造。
type Spec struct {
	// configuration 按声明顺序保存延迟加载器，须在配置 Manager 构造前完成登记。
	configuration []config.SourceLoader
	// application 借用共享应用声明，登记运行时和生命周期钩子。
	application *app.Spec
	// server 借用共享服务器声明，收集传输层配置。
	server *server.Spec
	// jobs 借用共享任务声明，收集定时及后台任务。
	jobs *job.Spec
}

// NewSpec 借用 Wire 注入的共享声明，不创建或复制领域 Spec。
// 根据 ConfigSources 固定环境并登记默认加载行为，不执行配置 I/O；同一组装链共享领域 Spec。
func NewSpec(application *app.Spec, servers *server.Spec, jobs *job.Spec, sources ConfigSources) *Spec {
	spec := &Spec{application: application, server: servers, jobs: jobs}
	if loader := sources.loader(); loader != nil {
		spec.Configuration(loader)
	}
	return spec
}

// Configuration 按顺序声明额外配置源；声明阶段不执行 I/O。
// 必须在提供 Spec 的构造函数中调用，不能放进依赖 Manager 的业务 Boot。
func (s *Spec) Configuration(loaders ...config.SourceLoader) *Spec {
	for _, loader := range loaders {
		if loader == nil {
			panic("bootstrap: config source loader is nil")
		}
	}
	s.configuration = append(s.configuration, loaders...)
	return s
}

// Http 返回业务 HTTP 声明；默认启用，配置 disable=true 可关闭业务监听。
func (s *Spec) Http() server.HTTPBuilder {
	return s.server.HTTP()
}

// Grpc 返回业务 gRPC 声明；有效服务注册默认启用，显式配置优先。
func (s *Spec) Grpc() server.GRPCBuilder {
	return s.server.GRPC()
}

// Health 返回独立的健康检查声明，不改变业务 HTTP 或 gRPC 的启用状态。
func (s *Spec) Health() *server.HealthBuilder {
	return s.server.Health()
}

// Job 返回任务声明，支持 Cron、Once 和 Daemon。
func (s *Spec) Job() job.Builder {
	return s.jobs
}

// RegisterRuntime 登记 Wire 构造的通用运行时，包括 queue.Worker、kafka.ConsumerRuntime 和自定义 worker。
// 立即写入共享 app.Spec；冻结后写入会 panic。App 管理 Start/Stop，cleanup 仍归该运行时的 provider。
func (s *Spec) RegisterRuntime(runtime app.Runtime) *Spec {
	s.application.RegisterRuntime(runtime)
	return s
}

// RegisterKafkaConsumer 登记已构造的非 nil Kafka ConsumerRuntime，返回同一 Spec 以支持链式调用。
// 复用 RegisterRuntime 的登记逻辑；App 管理 Start/Stop，cleanup 仍归原 provider。
func (s *Spec) RegisterKafkaConsumer(consumer *kafka.ConsumerRuntime) *Spec {
	return s.RegisterRuntime(consumer)
}

// AddContext 追加应用启动上下文的装饰函数。
func (s *Spec) AddContext(decorate app.ContextDecorator) *Spec {
	s.application.AddContext(decorate)
	return s
}

// AddMetadata 追加应用元数据。
func (s *Spec) AddMetadata(metadata map[string]string) *Spec {
	s.application.AddMetadata(metadata)
	return s
}

// AddEndpoints 追加对外公布的端点地址。
func (s *Spec) AddEndpoints(endpoints ...*url.URL) *Spec {
	s.application.AddEndpoints(endpoints...)
	return s
}

// AddSignals 指定触发应用退出的系统信号。
func (s *Spec) AddSignals(signals ...os.Signal) *Spec {
	s.application.AddSignals(signals...)
	return s
}

// BeforeStart 登记运行时启动前执行的业务钩子。
func (s *Spec) BeforeStart(hooks ...app.HookFunc) *Spec {
	s.application.BeforeStart(hooks...)
	return s
}

// AfterStart 登记所有运行时启动后执行的业务钩子。
func (s *Spec) AfterStart(hooks ...app.HookFunc) *Spec {
	s.application.AfterStart(hooks...)
	return s
}

// BeforeStop 登记运行时停止前执行的业务钩子。
func (s *Spec) BeforeStop(hooks ...app.HookFunc) *Spec {
	s.application.BeforeStop(hooks...)
	return s
}

// AfterStop 登记所有运行时停止后执行的业务钩子。
func (s *Spec) AfterStop(hooks ...app.HookFunc) *Spec {
	s.application.AfterStop(hooks...)
	return s
}
