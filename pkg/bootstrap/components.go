package bootstrap

import (
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// ComponentsBootstrap 标记 cmd 选择的组件已构造并登记到 app.Spec。
type ComponentsBootstrap struct{ stopDelay time.Duration }

// StopDelay 返回已选择服务器的停机延迟；无服务器时返回零。
func (b ComponentsBootstrap) StopDelay() time.Duration { return b.stopDelay }

// NewComponentsBootstrap 在业务 Boot 完成后同步组装选中的组件，不启动运行时或外部消费循环。
// Spec 只能组装一次；失败时释放本次创建的资源，调用方应丢弃本次 app.Spec。
// 成功后的 cleanup 归 Wire 所有，必须在应用停止后执行。
func NewComponentsBootstrap(
	spec *Spec,
	manager config.Manager,
	logger log.Logger,
	metricsProvider metrics.Provider,
	tracingProvider tracing.Provider,
	_ Bootstrap,
	coordinator job.ConcurrencyCoordinator,
) (ComponentsBootstrap, func(), error) {
	if spec == nil || spec.assembled {
		return ComponentsBootstrap{}, nil, fmt.Errorf("components bootstrap: spec is nil or already assembled")
	}
	application := ApplicationSpec(spec)
	spec.assembled = true
	cleanup := func() {}
	fail := func(err error) (ComponentsBootstrap, func(), error) {
		cleanup()
		return ComponentsBootstrap{}, nil, fmt.Errorf("components bootstrap: %w", err)
	}
	var result ComponentsBootstrap
	// 未选协议显式关闭，避免配置默认开启 worker 不需要的监听。
	if spec.server != nil {
		if !spec.http {
			spec.server.HTTP().Disable()
		}
		if !spec.grpc {
			spec.server.GRPC().Disable()
		}
		runtime, release, err := server.NewRuntime(manager, logger, metricsProvider, tracingProvider, spec.server)
		if err != nil {
			return fail(err)
		}
		cleanup = release
		result.stopDelay = runtime.StopDelay()
		if _, err := NewServerBootstrap(application, runtime); err != nil {
			return fail(err)
		}
	}
	if spec.jobs != nil {
		runtime, err := job.NewManager(logger, spec.jobs, tracingProvider, metricsProvider, coordinator)
		if err != nil {
			return fail(err)
		}
		if _, err := NewJobBootstrap(application, runtime); err != nil {
			return fail(err)
		}
	}
	for _, runtime := range spec.runtimes {
		if runtime == nil {
			return fail(fmt.Errorf("custom runtime is nil"))
		}
		if err := application.RegisterRuntime(runtime); err != nil {
			return fail(err)
		}
	}
	return result, cleanup, nil
}

// NewApplicationBootstrap 等待声明及组件登记完成后解除应用冻结屏障。
// 统一 Spec 模式使用此入口；NewBootstrap 保留给业务自行登记 Runtime 的旧模式。
func NewApplicationBootstrap(_ InfrastructureBootstrap, _ ComponentsBootstrap) StartupReady {
	return StartupReady{}
}
