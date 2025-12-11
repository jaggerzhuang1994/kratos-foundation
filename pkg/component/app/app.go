package app

import (
	"context"
	"syscall"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/job"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/websocket"
)

func NewApp(
	_ bootstrap.Bootstrap, // 禁止在 bootstrap 初始化 app
	_ *http.Server, // 初始化 http 服务器
	_ *grpc.Server, // 初始化 grpc 服务器
	_ *websocket.Server, // 初始化 ws 服务器
	appInfo *app_info.AppInfo,
	cfg *Config,
	log_ *log.Log, // log 组件
	metrics_ *metrics.Metrics, // metric 组件
	hook *Hook, // app hook
	jobServer *job.Server, // job server
	serverProvider *server.Register, // server提供者
	registrar registry.Registrar, // 服务注册中心实例
) *kratos.App {
	ctx := app_info.NewContext(context.Background(), appInfo)
	ctx = metrics.NewContext(ctx, metrics_)

	// initCtx
	for _, initCtx := range hook.initCtx {
		ctx = initCtx(ctx)
	}

	servers := serverProvider.GetServers()
	if jobServer != nil {
		servers = append(servers, jobServer)
	}

	var opts = []kratos.Option{
		kratos.ID(appInfo.GetId()),
		kratos.Name(appInfo.GetName()),
		kratos.Version(appInfo.GetVersion()),
		kratos.Metadata(appInfo.GetMetadata()),
		kratos.Logger(log_.GetLogger()),
		kratos.Context(ctx),
		kratos.Server(servers...),
		kratos.Signal(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM), // 平滑重启的信号量
		kratos.StopTimeout(cfg.GetStopTimeout().AsDuration()),
	}

	// app hook
	for _, beforeStart := range hook.beforeStart {
		opts = append(opts, kratos.BeforeStart(beforeStart))
	}
	for _, beforeStop := range hook.beforeStop {
		opts = append(opts, kratos.BeforeStop(beforeStop))
	}
	for _, afterStart := range hook.afterStart {
		opts = append(opts, kratos.AfterStart(afterStart))
	}
	for _, afterStop := range hook.afterStop {
		opts = append(opts, kratos.AfterStop(afterStop))
	}

	// service register
	if !cfg.GetDisableRegistrar() && registrar != nil {
		opts = append(opts, kratos.Registrar(registrar), kratos.RegistrarTimeout(cfg.GetRegistrarTimeout().AsDuration()))
	}

	// initOptions
	for _, initOptions := range hook.initOptions {
		opts = initOptions(opts)
	}

	return kratos.New(opts...)
}
