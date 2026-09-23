package wireassembly

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/bootstrap/consulconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationregistry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
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
	Server   *businessServer
}

// 业务阶段显式依赖基础设施完成标记，阶段内部不规定额外顺序。
func newBootstrap(_ bootstrap.InfrastructureBootstrap, routes *businessServer, spec *bootstrap.Spec, servers *server.Spec, decorate app.ContextDecorator) (bootstrap.Bootstrap, error) {
	servers.HTTP().Register(routes.register)
	if decorate != nil {
		spec.AddContext(decorate)
	}
	spec.AddMetadata(map[string]string{"business": "ready"})
	return bootstrap.Bootstrap{}, nil
}

// componentsBoot 同时声明任务和应用贡献，任务必须在 Boot 返回后才被组装。
func componentsBoot(_ bootstrap.InfrastructureBootstrap, components *bootstrap.Spec, jobs *job.Spec) (bootstrap.Bootstrap, error) {
	jobs.RegisterOnce("complete", job.TaskFunc(func(context.Context) error { return nil })).ExitWhenDone()
	components.AddMetadata(map[string]string{"boot": "declared"})
	return bootstrap.Bootstrap{}, nil
}

type driverAssembly struct {
	LocalPaths  bootstrap.LocalConfigPaths
	RemotePaths bootstrap.RemoteConfigPaths
	App         *kratos.App
	Client      client.Factory
	Registry    *foundationregistry.Factory
	Config      config.Manager
}

func customRemoteConfigDirs() consulconfig.RemoteConfigDirs {
	return consulconfig.RemoteConfigDirs{"configs", "secrets"}
}

func customRemoteConfigPatterns() consulconfig.RemoteConfigPaths {
	return consulconfig.RemoteConfigPaths{"shared-orders/{{env}}/shared-config.yaml"}
}
