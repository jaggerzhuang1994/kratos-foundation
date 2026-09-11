package bootstrap

import (
	"context"

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
	_ ConfigObservabilityBootstrap,
) InfrastructureBootstrap {
	return InfrastructureBootstrap{}
}

// Bootstrap 标记业务 Boot 已完成声明和应用贡献，由业务组装层提供 provider。
// 统一 Spec 模式下，组件在此标记之后构造；旧模式可先聚合独立组件贡献。
type Bootstrap struct{}

// StartupReady 标记全部组装阶段完成，可以创建应用。
type StartupReady struct{}

// NewBootstrap 汇合阶段依赖；业务阶段的前置依赖由用户 provider 声明。
func NewBootstrap(
	_ InfrastructureBootstrap,
	_ Bootstrap,
) StartupReady {
	return StartupReady{}
}

// NewKratosApp 在最终组装屏障之后构造 Kratos 应用。
// 内部创建根 Context，不通过 Wire 注入；业务上下文贡献使用 Spec.AddContext。
// 不接收提前构造的 App，确保 Spec 冻结发生在全部贡献登记之后。
func NewKratosApp(spec *app.Spec, _ StartupReady, config app.Config, stopPolicy *app.StopPolicy, registrar registry.Registrar) (*kratos.App, error) {
	application, err := app.NewApp(context.Background(), spec, config, stopPolicy, registrar)
	if err != nil {
		return nil, err
	}
	return application.App, nil
}
