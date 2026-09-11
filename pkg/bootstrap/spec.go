package bootstrap

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

// Spec 按领域收集应用声明。使用 NewSpec 构造，仅在组装前串行修改。
type Spec struct {
	application *app.Spec
	server      *server.Spec
	http, grpc  bool
	jobs        *job.Spec
	runtimes    []app.Runtime
	assembled   bool
}

// NewSpec 创建应用组件及生命周期声明。
func NewSpec() *Spec {
	return &Spec{application: app.NewSpec()}
}

// ApplicationSpec 向 Foundation provider 暴露同一应用状态，不应同时提供 app.NewSpec。
func ApplicationSpec(spec *Spec) *app.Spec { return spec.application }

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
