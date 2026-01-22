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
	"github.com/pkg/errors"
)

// ServerProvider 服务提供者接口，用于获取应用的所有传输层服务器
type ServerProvider interface {
	GetServers() []transport.Server
}

// NewApp 创建并配置 Kratos 应用实例
// 参数：
//   - bootstrap: 禁止在 bootstrap 中注入 app（防止循环依赖）
//   - config: 应用配置
//   - hook_: 应用生命周期钩子
//   - appInfo: 应用元信息（ID、名称、版本等）
//   - ctx: 应用上下文
//   - logger: 日志记录器
//   - serverProvider: 服务器提供者
//   - registrar: 服务注册中心
//
// 返回：配置好的 Kratos 应用实例或错误
func NewApp(
	_ bootstrap.Bootstrap, // 禁止在 bootstrap 注入 app
	config Config,
	hook_ Hook,
	appInfo app_info.AppInfo,
	ctx context.Context,
	logger log.Logger,
	serverProvider ServerProvider,
	registrar registry.Registrar,
) (*kratos.App, error) {
	var options []kratos.Option

	// 合并配置和 appInfo 的元数据
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

	// 配置服务发现端点
	if config.GetEndpoints() != nil {
		options = append(options, kratos.Endpoint(utils.Map(config.GetEndpoints(), func(e *config_pb.Endpoint) *url.URL {
			return &url.URL{Scheme: e.GetScheme(), Host: e.GetHost()}
		})...))
	}

	// 设置上下文和日志
	options = append(options, kratos.Context(ctx))
	options = append(options, kratos.Logger(logger))

	// 添加传输层服务器（HTTP、gRPC 等）
	options = append(options, kratos.Server(serverProvider.GetServers()...))

	// 配置平滑重启信号
	options = append(options, kratos.Signal(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM))

	// 配置服务注册中心
	if !config.GetDisableRegistrar() && registrar != nil {
		options = append(options,
			kratos.Registrar(registrar),
			kratos.RegistrarTimeout(config.GetRegistrarTimeout().AsDuration()),
		)
	}

	// 配置优雅停机超时时间
	options = append(options, kratos.StopTimeout(config.GetStopTimeout().AsDuration()))

	// 注册应用生命周期钩子
	if h, ok := hook_.(*hook); ok {
		// 启动前钩子
		for _, beforeStart := range h.beforeStartHooks {
			options = append(options, kratos.BeforeStart(beforeStart))
		}
		// 启动后钩子
		for _, afterStart := range h.afterStartHooks {
			options = append(options, kratos.AfterStart(afterStart))
		}
		// 停止前钩子
		for _, beforeStop := range h.beforeStopHooks {
			options = append(options, kratos.BeforeStop(beforeStop))
		}
		// 停止后钩子
		for _, afterStop := range h.afterStopHooks {
			options = append(options, kratos.AfterStop(afterStop))
		}
	} else {
		return nil, errors.New("app.Hook does not implement hook")
	}

	return kratos.New(options...), nil
}
