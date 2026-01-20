package server

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job"
)

type Register interface {
	GetServers() []transport.Server
	RegisterServer(server transport.Server)
}

type register struct {
	config  Config
	servers []transport.Server
}

func NewRegister(
	config Config,
	http HttpServer,
	grpc GrpcServer,
	job job.Server,
) Register {
	r := &register{
		config: config,
	}
	if http != nil {
		r.RegisterServer(http)
	}
	if grpc != nil {
		r.RegisterServer(grpc)
	}
	r.RegisterServer(job)
	return r
}

func (s *register) RegisterServer(server transport.Server) {
	// 如果存在对外暴露端点，且 stop_delay > 0，则套一层 serverStopDelayWrapper 来延迟停止服务
	// 避免注册到服务中心停止服务导致其他服务无法请求
	if endpointer, ok := server.(transport.Endpointer); ok && s.config.GetStopDelay().AsDuration() > 0 {
		s.servers = append(s.servers, &serverStopDelayWrapper{
			Server:     server,
			Endpointer: endpointer,
			stopDelay:  s.config.GetStopDelay().AsDuration(),
		})
	} else {
		s.servers = append(s.servers, server)
	}
}

func (s *register) GetServers() []transport.Server {
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
