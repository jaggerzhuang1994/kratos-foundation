package client

import (
	"context"
	"sync"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/discovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc"
)

type HTTPClient = *http.Client
type GRPCClient = *grpc.ClientConn

type Factory interface {
	ResolveClient(ctx context.Context) (httpClient HTTPClient, grpcClient GRPCClient, err error)
	MakeGrpcConn(ctx context.Context) (grpcClient GRPCClient, err error)
	MakeHttpClient(ctx context.Context) (httpClient HTTPClient, err error)
}

type factory struct {
	log.Log
	log       log.Log
	tracing   tracing.Tracing
	metrics   metrics.Metrics
	discovery discovery.Discovery

	clientOptions map[string]Option
	// 初始化连接的锁
	initLocker sync.Mutex
	// 客户端缓存 map[ clientKey ] -> HTTPClient | GRPCClient
	httpClients sync.Map
	grpcClients sync.Map
}

func NewFactory(
	config Config,
	log log.Log,
	tracing tracing.Tracing,
	metrics metrics.Metrics,
	discovery discovery.Discovery,
) Factory {
	return &factory{
		Log:           log.WithModule("client", config.GetLog()),
		log:           log,
		tracing:       tracing,
		metrics:       metrics,
		discovery:     discovery,
		clientOptions: config.GetClients(),
	}
}

func (f *factory) MakeGrpcConn(ctx context.Context) (grpcClient GRPCClient, err error) {
	_, grpcClient, err = f.resolveClient(ctx, config_pb.Protocol_GRPC)
	return
}

func (f *factory) MakeHttpClient(ctx context.Context) (httpClient HTTPClient, err error) {
	httpClient, _, err = f.resolveClient(ctx, config_pb.Protocol_HTTP)
	return
}

func (f *factory) ResolveClient(ctx context.Context) (httpClient HTTPClient, grpcClient GRPCClient, err error) {
	return f.resolveClient(ctx)
}

func (f *factory) resolveClient(ctx context.Context, optionalProtocol ...config_pb.Protocol) (httpClient HTTPClient, grpcClient GRPCClient, err error) {
	clientName := ConnNameFromContext(ctx)
	if clientName == "" {
		err = ErrInvalidClientName
		return
	}
	clientKey := newClientConfig(clientName, f.clientOptions[clientName], optionalProtocol...)

	// 从缓存读
	getCache := func() bool {
		if clientKey.protocol == config_pb.Protocol_GRPC || clientKey.protocol == config_pb.Protocol_GRPCS {
			conn, ok := f.grpcClients.Load(clientKey)
			if !ok {
				return false
			}
			grpcClient = conn.(GRPCClient)
			return true
		}
		if clientKey.protocol == config_pb.Protocol_HTTP || clientKey.protocol == config_pb.Protocol_HTTPS {
			conn, ok := f.httpClients.Load(clientKey)
			if !ok {
				return false
			}
			httpClient = conn.(HTTPClient)
			return true
		}
		// 未知协议，则报错
		err = ErrInvalidProtocol
		return true
	}

	// 从缓存读取链接，返回ok表示缓存存在
	ok := getCache()
	if ok {
		return
	}
	if err != nil {
		return
	}

	f.initLocker.Lock()
	defer f.initLocker.Unlock()
	// 如果2个同时阻塞锁，一个初始化完链接后，另一个可以再读一次缓存
	// 从缓存读取链接，返回ok表示缓存存在
	ok = getCache()
	if ok {
		return
	}
	if err != nil {
		return
	}

	// 初始化客户端
	f.Infof("client initializing, name=%s protocol=%s target=%s", clientKey.name, clientKey.protocol.String(), clientKey.option.GetTarget())
	defer func() {
		if err == nil {
			f.Infof("client initialized, name=%s", clientKey.name)
		} else {
			f.Errorf("client init failed, name=%s err=%v", clientKey.name, err)
		}
	}()
	// 初始化 grpc 连接
	if clientKey.protocol == config_pb.Protocol_GRPC || clientKey.protocol == config_pb.Protocol_GRPCS {
		grpcClient, err = f.newGRPCClient(ctx, clientKey)
		if err != nil {
			return
		}
		f.grpcClients.Store(clientKey, grpcClient)
		return
	}
	if clientKey.protocol == config_pb.Protocol_HTTP || clientKey.protocol == config_pb.Protocol_HTTPS {
		// 初始化 http 连接
		httpClient, err = f.newHTTPClient(ctx, clientKey)
		if err != nil {
			return
		}
		f.httpClients.Store(clientKey, httpClient)
	}
	return
}
