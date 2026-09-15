package bootstrap

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

// ServerBootstrap 标记业务声明的服务器已构造并登记。
type ServerBootstrap struct{}

// NewServerBootstrap 等待业务 Boot 完成后构造选中的协议，并登记到应用 Spec。
// 未选择服务器时不创建资源；cleanup 由 Wire 在应用停止后逆序调用。
func NewServerBootstrap(
	spec *Spec,
	manager config.Manager,
	logger log.Logger,
	meter metrics.Provider,
	tracer tracing.Provider,
	_ Bootstrap,
) (ServerBootstrap, func(), error) {
	if spec == nil || spec.assembled || spec.serverBuilt {
		return ServerBootstrap{}, nil, fmt.Errorf("server bootstrap: spec is nil or already assembled")
	}
	spec.serverBuilt = true
	if spec.server == nil {
		return ServerBootstrap{}, func() {}, nil
	}
	// 未选协议显式关闭，避免配置默认开启 worker 不需要的监听。
	if !spec.http {
		spec.server.HTTP().Disable()
	}
	if !spec.grpc {
		spec.server.GRPC().Disable()
	}
	runtime, cleanup, err := server.NewRuntime(manager, logger, meter, tracer, spec.server)
	if err != nil {
		return ServerBootstrap{}, nil, fmt.Errorf("server bootstrap: %w", err)
	}
	if err := registerServer(ApplicationSpec(spec), runtime); err != nil {
		cleanup()
		return ServerBootstrap{}, nil, err
	}
	return ServerBootstrap{}, cleanup, nil
}

// registerServer 将业务监听和管理监听登记到应用 Spec，由组件组装统一调用。
func registerServer(spec *app.Spec, runtime *server.Runtime) error {
	runtime.SetReadinessSource(spec.Ready)
	httpRuntime, grpcRuntime := runtime.Servers()
	if httpRuntime != nil {
		if err := spec.RegisterRuntime(httpRuntime); err != nil {
			return fmt.Errorf("server bootstrap: register server.http runtime: %w", err)
		}
	}
	if grpcRuntime != nil {
		if err := spec.RegisterRuntime(grpcRuntime); err != nil {
			return fmt.Errorf("server bootstrap: register server.grpc runtime: %w", err)
		}
	}
	for _, management := range runtime.ManagementServers() {
		if err := spec.RegisterRuntime(management); err != nil {
			return fmt.Errorf("server bootstrap: register monitoring HTTP runtime: %w", err)
		}
	}
	return nil
}
