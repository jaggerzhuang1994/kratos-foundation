package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// ServerBootstrap 标记业务声明的服务器已构造并登记。
type ServerBootstrap struct{}

// JobBootstrap 标记 Job Manager 已同步登记到应用 Spec。
type JobBootstrap struct{}

// NewServerBootstrap 等待业务 Boot 完成后按声明和配置构造业务及管理监听，并登记到应用 Spec。
// 业务 HTTP 默认开启；cleanup 由 Wire 在应用停止后逆序调用。
func NewServerBootstrap(
	application *app.Spec,
	servers *server.Spec,
	manager config.Manager,
	logger log.Logger,
	meter metrics.Provider,
	tracer tracing.Provider,
) (ServerBootstrap, func(), error) {
	runtime, cleanup, err := server.NewRuntime(manager, logger, meter, tracer, servers)
	if err != nil {
		return ServerBootstrap{}, nil, fmt.Errorf("server bootstrap: %w", err)
	}
	// 登记 panic 时仍释放本 provider 刚构造的资源，不拦截或转换组装错误。
	registered := false
	defer func() {
		if !registered {
			cleanup()
		}
	}()

	runtime.SetReadinessSource(application.Ready)
	httpRuntime, grpcRuntime := runtime.Servers()
	if httpRuntime != nil {
		application.RegisterRuntime(httpRuntime)
	}
	if grpcRuntime != nil {
		application.RegisterRuntime(grpcRuntime)
	}
	for _, management := range runtime.ManagementServers() {
		application.RegisterRuntime(management)
	}

	registered = true
	return ServerBootstrap{}, cleanup, nil
}

// NewJobBootstrap 在 Server 完成后构造并登记选中的任务，不启动任务。
// 无任务时不登记 Runtime；由 Wire 保证构造顺序与单次调用，失败后应丢弃 Spec。
func NewJobBootstrap(
	application *app.Spec,
	jobs *job.Spec,
	configuration config.Manager,
	logger log.Logger,
	meter metrics.Provider,
	tracer tracing.Provider,
) (JobBootstrap, error) {
	manager, err := job.NewManager(logger, jobs, tracer, meter, configuration)
	if err != nil {
		return JobBootstrap{}, fmt.Errorf("job bootstrap: %w", err)
	}
	if !manager.HasJobs() {
		return JobBootstrap{}, nil
	}
	application.RegisterRuntime(&jobRuntime{Manager: manager, application: application})
	return JobBootstrap{}, nil
}

// jobRuntime 在组装边界将任务完成转换为应用正常停止，领域包无需了解 App。
type jobRuntime struct {
	// Manager 借用任务管理器，将任务生命周期适配为应用运行时。
	*job.Manager
	// application 提供 Kratos 进入 AfterStart 且启动后钩子完成后的 ready 屏障。
	application *app.Spec
}

func (r *jobRuntime) Start(ctx context.Context) error {
	if err := r.application.WaitReady(ctx); err != nil {
		return err
	}
	err := r.Manager.Start(ctx)
	// Manager 的完成信号为独立哨兵；包含该哨兵的任务错误仍应原样传播。
	if errors.Is(err, job.ErrCompleted) {
		return app.ErrStopRequested
	}
	return err
}
