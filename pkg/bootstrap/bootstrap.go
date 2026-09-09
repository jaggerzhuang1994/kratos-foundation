package bootstrap

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
)

// InfrastructureBootstrap 标记身份、日志、追踪和指标的基础贡献已完成。
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

// UserBootstrap 标记用户业务贡献已完成，由业务组装层提供 provider。
// 用户 provider 接收 InfrastructureBootstrap，并聚合所需的业务组件贡献。
type UserBootstrap struct{}

// Bootstrap 标记基础设施与用户业务阶段均已完成。
type Bootstrap struct{}

// NewBootstrap 汇合阶段依赖；业务阶段的前置依赖由用户 provider 声明。
func NewBootstrap(
	_ InfrastructureBootstrap,
	_ UserBootstrap,
) Bootstrap {
	return Bootstrap{}
}

// NewKratosApp 在最终组装屏障之后构造 Kratos 应用。
// 不接收提前构造的 App，确保 Spec 冻结发生在全部贡献登记之后。
func NewKratosApp(ctx context.Context, spec *app.Spec, _ Bootstrap, config app.Config, stopPolicy *app.StopPolicy) (*kratos.App, error) {
	application, err := app.NewApp(ctx, spec, config, stopPolicy)
	if err != nil {
		return nil, err
	}
	return application.App, nil
}
