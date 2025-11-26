package app

import (
	"context"
	"syscall"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

func NewApp(
	_ bootstrap.Bootstrap,
	appInfo *kratos_foundation_pb.AppInfo,
	cfg *Config,
	l *log.Log, // log 组件
	hook *HookManager, // app hook
	metrics *metric.Metrics, // metric 组件
	serverManager *server.Manager, // server 组件
	registrar registry.Registrar, // 服务注册中心实例
) (*kratos.App, error) {
	ctx := app_info.NewContext(context.Background(), appInfo)
	ctx = log.NewContext(ctx, l)
	ctx = metric.NewContext(ctx, metrics)

	// initCtx
	for _, initCtx := range hook.initCtx {
		ctx = initCtx(ctx)
	}

	var opts = []kratos.Option{
		kratos.ID(appInfo.GetId()),
		kratos.Name(appInfo.GetName()),
		kratos.Version(appInfo.GetVersion()),
		kratos.Metadata(appInfo.GetMetadata()),
		kratos.Context(ctx),
		kratos.Logger(l.GetLogger()),
		kratos.Server(serverManager.GetServers()...),
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

	if !cfg.GetDisableRegistrar() && registrar != nil {
		opts = append(opts, kratos.Registrar(registrar), kratos.RegistrarTimeout(cfg.GetRegistrarTimeout().AsDuration()))
	}

	// initOptions
	for _, initOptions := range hook.initOptions {
		opts = initOptions(opts)
	}

	return kratos.New(opts...), nil
}
