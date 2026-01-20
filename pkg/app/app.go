package app

import (
	"context"
	"net/url"
	"syscall"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
)

// ServerProvider 服务提供者
type ServerProvider interface {
	GetServers() []transport.Server
}

func NewApp(
	_ bootstrap.Bootstrap, // 禁止在 bootstrap 注入 app
	config Config,
	hook_ Hook,
	appInfo app_info.AppInfo,
	ctx context.Context,
	logger log.Logger,
	serverProvider ServerProvider,
	registrar registry.Registrar,
) *kratos.App {
	var options []kratos.Option

	// app info
	var md = map[string]string{}
	for k, v := range config.GetMetadata() {
		md[k] = v
	}
	for k, v := range appInfo.GetMetadata() {
		md[k] = v
	}
	options = append(options,
		kratos.ID(appInfo.GetId()),
		kratos.Name(appInfo.GetName()),
		kratos.Version(appInfo.GetVersion()),
		kratos.Metadata(md),
	)

	// endpoints
	if config.GetEndpoints() != nil {
		options = append(options, kratos.Endpoint(utils.Map(config.GetEndpoints(), func(e *config_pb.Endpoint) *url.URL {
			return &url.URL{Scheme: e.GetScheme(), Host: e.GetHost()}
		})...))
	}

	// ctx
	options = append(options, kratos.Context(ctx))
	// logger
	options = append(options, kratos.Logger(logger))

	// server
	options = append(options, kratos.Server(serverProvider.GetServers()...))

	// signal 平滑重启的信号量
	options = append(options, kratos.Signal(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM))

	// register
	if !config.GetDisableRegistrar() && registrar != nil {
		options = append(options,
			kratos.Registrar(registrar),
			kratos.RegistrarTimeout(config.GetRegistrarTimeout().AsDuration()),
		)
	}

	// stop timeout
	options = append(options, kratos.StopTimeout(config.GetStopTimeout().AsDuration()))

	// app hook
	for _, beforeStart := range hook_.(*hook).BeforeStartHooks {
		options = append(options, kratos.BeforeStart(beforeStart))
	}
	for _, afterStart := range hook_.(*hook).AfterStartHooks {
		options = append(options, kratos.AfterStart(afterStart))
	}
	for _, beforeStop := range hook_.(*hook).BeforeStopHooks {
		options = append(options, kratos.BeforeStop(beforeStop))
	}
	for _, afterStop := range hook_.(*hook).AfterStopHooks {
		options = append(options, kratos.AfterStop(afterStop))
	}

	return kratos.New(options...)
}
