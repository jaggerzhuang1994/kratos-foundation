package wireassembly

import (
	"context"
	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

type assembly struct {
	App      *kratos.App
	Spec     *app.Spec
	Info     appinfo.AppInfo
	Manager  config.Manager
	Logger   log.Logger
	Metrics  metrics.Provider
	Tracing  tracing.Provider
	Tracer   tracing.Tracing
	Database database.Manager
	Server   *server.Runtime
}

// 业务阶段显式依赖基础设施完成标记，阶段内部不规定额外顺序。
func newBootstrap(_ bootstrap.InfrastructureBootstrap, _ bootstrap.ServerBootstrap, spec *app.Spec) (bootstrap.Bootstrap, error) {
	return bootstrap.Bootstrap{}, spec.AddMetadata(map[string]string{"business": "ready"})
}

// componentsBoot 同时声明任务和应用贡献，任务必须在 Boot 返回后才被组装。
func componentsBoot(_ bootstrap.InfrastructureBootstrap, components *bootstrap.Spec) (bootstrap.Bootstrap, error) {
	components.Job().RegisterOnce("complete", job.TaskFunc(func(context.Context) error { return nil })).ExitWhenDone()
	return bootstrap.Bootstrap{}, components.AddMetadata(map[string]string{"boot": "declared"})
}
