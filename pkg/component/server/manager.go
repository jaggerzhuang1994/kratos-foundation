package server

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

type Manager struct {
	cfg     *Config
	servers []transport.Server
}

func NewManager(
	cfg *Config,
	// 无论如何初始化 http/grpc server，会动态根据配置返回 nil/实例
	httpServer *http.Server,
	grpcServer *grpc.Server,
) *Manager {
	mgr := &Manager{
		cfg: cfg,
	}

	if httpServer != nil {
		mgr.RegisterServer(httpServer)
	}

	if grpcServer != nil {
		mgr.RegisterServer(grpcServer)
	}

	return mgr
}

func (s *Manager) RegisterServer(server transport.Server) {
	// 如果存在对外暴露端点，且 stop_delay > 0，则套一层 serverStopDelayWrapper 来延迟停止服务
	// 避免注册到服务中心停止服务导致其他服务无法请求
	if endpointer, ok := server.(transport.Endpointer); ok && s.cfg.GetStopDelay().AsDuration() > 0 {
		s.servers = append(s.servers, &serverStopDelayWrapper{
			Server:     server,
			Endpointer: endpointer,
			stopDelay:  s.cfg.GetStopDelay().AsDuration(),
		})
	} else {
		s.servers = append(s.servers, server)
	}
}

func (s *Manager) GetServers() []transport.Server {
	return s.servers
}

func (s *Manager) GetStopDelay() time.Duration {
	return s.cfg.GetStopDelay().AsDuration()
}

type serverStopDelayWrapper struct {
	transport.Server
	transport.Endpointer
	stopDelay time.Duration
}

func (s *serverStopDelayWrapper) Stop(ctx context.Context) error {
	if s.stopDelay > 0 {
		time.Sleep(s.stopDelay)
	}
	return s.Server.Stop(ctx)
}
