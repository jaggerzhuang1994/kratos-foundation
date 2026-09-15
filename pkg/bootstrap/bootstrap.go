package bootstrap

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
)

// InfrastructureBootstrap 标记身份、日志、追踪、指标和配置观测的基础贡献已完成。
type InfrastructureBootstrap struct{}

// NewInfrastructureBootstrap 聚合基础设施贡献，不规定阶段内组件的顺序。
func NewInfrastructureBootstrap(
	_ AppInfoBootstrap,
	_ LogBootstrap,
	_ TracingBootstrap,
	_ MetricsBootstrap,
) InfrastructureBootstrap {
	return InfrastructureBootstrap{}
}

// Bootstrap 标记业务 Boot 已完成声明和应用贡献，由业务组装层提供 provider。
// 组件在此标记之后构造；自行构造的 Runtime 贡献也须在业务 provider 返回前完成。
type Bootstrap struct{}

// RuntimeBootstrap 标记服务器、任务及自定义 Runtime 已完成构造和登记。
type RuntimeBootstrap struct{}

// NewRuntimeBootstrap 等待 Server 和 Job 完成组装，再登记自定义 Runtime。
// 不构造资源或返回 cleanup；前置组件的资源由 Wire 负责逆序释放。
func NewRuntimeBootstrap(spec *Spec, _ ServerBootstrap, _ JobBootstrap) (RuntimeBootstrap, error) {
	if spec == nil || spec.assembled {
		return RuntimeBootstrap{}, fmt.Errorf("runtime bootstrap: spec is nil or already assembled")
	}
	spec.assembled = true
	for _, runtime := range spec.runtimes {
		if runtime == nil {
			return RuntimeBootstrap{}, fmt.Errorf("runtime bootstrap: custom runtime is nil")
		}
		if err := ApplicationSpec(spec).RegisterRuntime(runtime); err != nil {
			return RuntimeBootstrap{}, fmt.Errorf("runtime bootstrap: %w", err)
		}
	}
	return RuntimeBootstrap{}, nil
}

// StartupReady 标记全部组装阶段完成，可以创建应用。
type StartupReady struct{}

// NewApplicationBootstrap 等待声明及组件登记完成后解除应用冻结屏障。
// 唯一的 StartupReady 构造入口，组件构造通过依赖链等待业务 Boot 完成。
func NewApplicationBootstrap(_ InfrastructureBootstrap, _ RuntimeBootstrap) StartupReady {
	return StartupReady{}
}

// NewKratosApp 在最终组装屏障之后构造 Kratos 应用。
// 内部创建根 Context，不通过 Wire 注入；业务上下文贡献使用 Spec.AddContext。
// 不接收提前构造的 App，确保 Spec 冻结发生在全部贡献登记之后。
func NewKratosApp(
	spec *app.Spec,
	config app.Config,
	stopPolicy *app.StopPolicy,
	registrar registry.Registrar,
	_ StartupReady,
) (*kratos.App, error) {
	application, err := app.NewApp(context.Background(), spec, config, stopPolicy, registrar)
	if err != nil {
		return nil, err
	}
	return application.App, nil
}
