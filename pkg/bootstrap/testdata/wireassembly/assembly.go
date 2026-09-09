package wireassembly

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
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
	Shared   *log.SharedState
	Logger   log.Logger
	Metrics  metrics.Provider
	Tracing  tracing.Provider
	Tracer   tracing.Tracing
	Database database.Manager
	Server   *server.Runtime
}

// 业务阶段显式依赖基础设施完成标记，阶段内部不规定额外顺序。
func newBootstrap(_ bootstrap.InfrastructureBootstrap, _ bootstrap.ServerBootstrap, _ bootstrap.ConfigObservabilityBootstrap, spec *app.Spec) (bootstrap.UserBootstrap, error) {
	return bootstrap.UserBootstrap{}, spec.AddMetadata(map[string]string{"business": "ready"})
}
