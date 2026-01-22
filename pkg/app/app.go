// Package app 提供应用创建和配置的核心功能。
//
// 此包整合了 Kratos 框架的应用实例创建、配置管理、Hook 机制
// 等功能，提供一个统一的应用构建入口。
//
// 主要功能：
//   - 创建和配置 Kratos 应用实例
//   - 合并应用配置和元信息
//   - 注册应用生命周期 Hook
//   - 配置服务注册与发现
//   - 集成 HTTP/gRPC 服务器
//   - 配置优雅停机和平滑重启
//
// 使用方式：
//   应用通常通过依赖注入（Wire）创建，NewApp 函数接收所有必要的依赖，
//   并返回一个完全配置好的 Kratos 应用实例。
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
//
// 此接口允许应用提供多个传输层服务器（HTTP、gRPC、WebSocket 等）。
// 通常由 Wire 依赖注入自动实现，返回所有已配置的服务器实例。
//
// 典型实现：
//   type ServerProvider struct {
//       httpServer *http.Server
//       grpcServer *grpc.Server
//   }
//
//   func (p *ServerProvider) GetServers() []transport.Server {
//       return []transport.Server{p.httpServer, p.grpcServer}
//   }
type ServerProvider interface {
	// GetServers 返回应用的所有传输层服务器
	//
	// 返回的服务器列表可以包含：
	//   - HTTP Server (REST API)
	//   - gRPC Server (RPC 服务)
	//   - WebSocket Server (实时通信)
	//   - 其他自定义服务器
	//
	// 注意：
	//   - 服务器会按照列表顺序启动和停止
	//   - 建议将 HTTP 服务器放在前面，便于健康检查
	GetServers() []transport.Server
}

// NewApp 创建并配置 Kratos 应用实例
//
// 此函数是应用构建的核心入口，整合了所有配置和依赖，
// 创建一个完全配置好的 Kratos 应用实例。
//
// 参数说明：
//   - bootstrap: 业务引导接口，用于初始化业务逻辑
//                ⚠️ 禁止在 bootstrap 中注入 app（防止循环依赖）
//   - config:     应用配置（从配置文件加载）
//   - hook_:      应用生命周期 Hook（启动前/后、停止前/后）
//   - appInfo:    应用元信息（ID、名称、版本等）
//   - ctx:        应用根上下文，所有请求都从此派生
//   - logger:     日志记录器，用于记录应用日志
//   - serverProvider: 服务器提供者，提供 HTTP/gRPC 等服务器
//   - registrar:  服务注册中心（Consul 等），用于服务发现
//
// 配置流程：
//   1. 合并应用元信息（配置文件 + appInfo）
//   2. 配置服务发现端点
//   3. 设置上下文和日志
//   4. 注册传输层服务器
//   5. 配置平滑重启信号（SIGINT、SIGTERM 等）
//   6. 配置服务注册中心
//   7. 配置优雅停机超时
//   8. 注册应用生命周期 Hook
//
// 返回：
//   - *kratos.App: 完全配置好的 Kratos 应用实例
//   - error:       配置过程中的错误
//
// 使用示例：
//   app, err := NewApp(
//       bootstrap,
//       config,
//       hook,
//       appInfo,
//       context.Background(),
//       logger,
//       serverProvider,
//       registrar,
//   )
//   if err != nil {
//       log.Fatal(err)
//   }
//
//   if err := app.Run(); err != nil {
//       log.Fatal(err)
//   }
//
// 注意事项：
//   - 此函数通常由 Wire 依赖注入自动调用
//   - Hook 函数会按照注册顺序执行
//   - 元数据合并规则：appInfo 覆盖配置文件中的值
//   - 服务注册需要配置 Registrar 和启用注册功能
func NewApp(
	_ bootstrap.Bootstrap, // 禁止在 bootstrap 注入 app（防止循环依赖）
	config Config,
	hook_ Hook,
	appInfo app_info.AppInfo,
	ctx context.Context,
	logger log.Logger,
	serverProvider ServerProvider,
	registrar registry.Registrar,
) (*kratos.App, error) {
	var options []kratos.Option

	// ============================================================
	// 步骤 1: 合并配置和 appInfo 的元数据
	// ============================================================
	// 元数据会注册到服务发现中心，供服务消费者使用
	// 合并规则：appInfo 的元数据会覆盖配置文件中的同名值
	var md = map[string]string{}
	for k, v := range config.GetMetadata() {
		md[k] = v
	}
	for k, v := range appInfo.GetMetadata() {
		md[k] = v
	}
	options = append(options,
		kratos.ID(appInfo.GetId()),                  // 应用唯一标识
		kratos.Name(appInfo.GetName()),              // 应用名称
		kratos.Version(appInfo.GetVersion()),        // 应用版本
		kratos.Metadata(md),                         // 应用元数据
	)

	// ============================================================
	// 步骤 2: 配置服务发现端点
	// ============================================================
	// 端点信息用于服务发现，告诉其他服务如何访问此服务
	// 例如：http://user-service:8000, grpc://user-service:9000
	if config.GetEndpoints() != nil {
		options = append(options, kratos.Endpoint(utils.Map(config.GetEndpoints(), func(e *config_pb.Endpoint) *url.URL {
			return &url.URL{Scheme: e.GetScheme(), Host: e.GetHost()}
		})...))
	}

	// ============================================================
	// 步骤 3: 设置上下文和日志
	// ============================================================
	// Context 是所有请求的根上下文，Logger 用于应用级日志记录
	options = append(options, kratos.Context(ctx))
	options = append(options, kratos.Logger(logger))

	// ============================================================
	// 步骤 4: 添加传输层服务器（HTTP、gRPC 等）
	// ============================================================
	// 服务器会按照提供的顺序启动
	// 建议将 HTTP 服务器放在前面，便于健康检查
	options = append(options, kratos.Server(serverProvider.GetServers()...))

	// ============================================================
	// 步骤 5: 配置平滑重启信号
	// ============================================================
	// 监听以下信号，触发优雅停机：
	//   - SIGINT (Ctrl+C)
	//   - SIGTERM (kill 命令)
	//   - SIGQUIT (kill -3)
	//   - SIGHUP (终端断开)
	options = append(options, kratos.Signal(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM))

	// ============================================================
	// 步骤 6: 配置服务注册中心
	// ============================================================
	// 如果启用了服务注册（DisableRegistrar=false）且提供了 Registrar
	// 则将服务注册到 Consul 等注册中心
	if !config.GetDisableRegistrar() && registrar != nil {
		options = append(options,
			kratos.Registrar(registrar),                                    // 注册中心实例
			kratos.RegistrarTimeout(config.GetRegistrarTimeout().AsDuration()), // 注册超时时间
		)
	}

	// ============================================================
	// 步骤 7: 配置优雅停机超时时间
	// ============================================================
	// 当收到停止信号后，应用有最多 StopTimeout 的时间来优雅关闭
	// 超时后会强制终止
	options = append(options, kratos.StopTimeout(config.GetStopTimeout().AsDuration()))

	// ============================================================
	// 步骤 8: 注册应用生命周期 Hook
	// ============================================================
	// 将 Hook 机制中注册的钩子函数应用到 Kratos 应用生命周期中
	// 钩子函数会按照注册顺序依次执行
	if h, ok := hook_.(*hook); ok {
		// 启动前钩子：在服务器启动之前执行
		// 适用场景：检查依赖、预热缓存、验证配置等
		for _, beforeStart := range h.beforeStartHooks {
			options = append(options, kratos.BeforeStart(beforeStart))
		}

		// 启动后钩子：在服务器启动完成并开始接受请求后执行
		// 适用场景：发送启动通知、注册到服务发现、启动后台任务等
		for _, afterStart := range h.afterStartHooks {
			options = append(options, kratos.AfterStart(afterStart))
		}

		// 停止前钩子：在服务器停止之前执行
		// 适用场景：优雅关闭连接、释放资源、持久化数据等
		for _, beforeStop := range h.beforeStopHooks {
			options = append(options, kratos.BeforeStop(beforeStop))
		}

		// 停止后钩子：在服务器完全停止后执行
		// 适用场景：清理临时文件、发送停止通知、关闭日志文件等
		for _, afterStop := range h.afterStopHooks {
			options = append(options, kratos.AfterStop(afterStop))
		}
	} else {
		return nil, errors.New("app.Hook does not implement hook")
	}

	// ============================================================
	// 创建并返回 Kratos 应用实例
	// ============================================================
	return kratos.New(options...), nil
}
