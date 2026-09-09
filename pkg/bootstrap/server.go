package bootstrap

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

// ServerBootstrap 标记 Server 已同步登记到应用 Spec。
type ServerBootstrap struct{}

// NewServerBootstrap 将启用的 HTTP 和 gRPC Runtime 登记到应用 Spec。
func NewServerBootstrap(spec *app.Spec, runtime *server.Runtime) (ServerBootstrap, error) {
	runtime.SetReadinessSource(spec.Ready)
	httpRuntime, grpcRuntime := runtime.Servers()
	if httpRuntime != nil {
		if err := spec.RegisterRuntime(httpRuntime); err != nil {
			return ServerBootstrap{}, fmt.Errorf("server bootstrap: register server.http runtime: %w", err)
		}
	}
	if grpcRuntime != nil {
		if err := spec.RegisterRuntime(grpcRuntime); err != nil {
			return ServerBootstrap{}, fmt.Errorf("server bootstrap: register server.grpc runtime: %w", err)
		}
	}
	for _, management := range runtime.ManagementServers() {
		if err := spec.RegisterRuntime(management); err != nil {
			return ServerBootstrap{}, fmt.Errorf("server bootstrap: register monitoring HTTP runtime: %w", err)
		}
	}
	return ServerBootstrap{}, nil
}
