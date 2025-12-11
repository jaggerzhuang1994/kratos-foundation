package server

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
)

type Register struct {
	cfg     *Config
	servers []transport.Server
}

func NewRegister(
	cfg *Config,
) *Register {
	mgr := &Register{
		cfg: cfg,
	}
	return mgr
}

func (s *Register) RegisterServer(server transport.Server) {
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

func (s *Register) GetServers() []transport.Server {
	return s.servers
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
