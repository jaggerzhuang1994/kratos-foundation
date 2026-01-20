package client

import (
	"context"
	"sync"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/discovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"google.golang.org/grpc"
)

type HTTPClient = *http.Client
type GRPCClient = *grpc.ClientConn

type Factory interface {
	ResolveClient(ctx context.Context) (httpClient HTTPClient, grpcClient GRPCClient, err error)
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
	clients sync.Map
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

func (f *factory) ResolveClient(ctx context.Context) (httpClient HTTPClient, grpcClient GRPCClient, err error) {
	clientKey := ClientConfig{Name: ConnNameFromContext(ctx)}
	if clientKey.Name == "" {
		err = ErrInvalidClientName
		return
	}
	clientKey.Option = f.clientOptions[clientKey.Name]

	getCache := func() bool {
		conn, ok := f.clients.Load(clientKey)
		if !ok {
			return false
		}

		if clientKey.IsGRPC() {
			grpcClient, ok = conn.(GRPCClient)
			if !ok {
				err = ErrInvalidGRPCClient
			}
			return true
		} else if clientKey.IsHTTP() {
			httpClient, ok = conn.(HTTPClient)
			if !ok {
				err = ErrInvalidHTTPClient
			}
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

	f.initLocker.Lock()
	defer f.initLocker.Unlock()
	// 如果2个同时阻塞锁，一个初始化完链接后，另一个可以再读一次缓存
	// 从缓存读取链接，返回ok表示缓存存在
	ok = getCache()
	if ok {
		return
	}

	// 初始化客户端
	f.Infof("client initializing, name=%s protocol=%s target=%s", clientKey.Name, clientKey.Option.GetProtocol().String(), clientKey.Option.GetTarget())
	defer func() {
		if err == nil {
			f.Infof("client initialized, name=%s", clientKey.Name)
		} else {
			f.Errorf("client init failed, name=%s err=%v", clientKey.Name, err)
		}
	}()
	// 初始化 grpc 连接
	if clientKey.IsGRPC() {
		grpcClient, err = f.newGRPCClient(ctx, clientKey)
		if err != nil {
			return
		}
		f.clients.Store(clientKey, grpcClient)
		return
	}
	// 初始化 http 连接
	httpClient, err = f.newHTTPClient(ctx, clientKey)
	if err != nil {
		return
	}
	f.clients.Store(clientKey, httpClient)
	return
}
