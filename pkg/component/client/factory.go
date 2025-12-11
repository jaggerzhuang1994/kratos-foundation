package client

import (
	"context"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

type HttpClient = *http.Client
type GrpcClient = grpc.ClientConnInterface
type Timeout = time.Duration

type Factory struct {
	log           *log.Log
	clientOptions map[string]*ClientOption
	discovery     registry.Discovery
	tracing       *tracing.Tracing
	metrics       *metrics.Metrics

	// 初始化链接的锁
	initLocker sync.Mutex
	// 链接缓存
	conn sync.Map
}

var ErrInvalidClientName = errors.New("client name is invalid")
var ErrInvalidGrpcClient = errors.New("invalid grpc client")
var ErrInvalidHttpClient = errors.New("invalid http client")
var ErrInvalidProtocol = errors.New("invalid client protocol")

func NewFactory(
	config *Config,
	log *log.Log,
	discovery registry.Discovery,
	tracing *tracing.Tracing,
	metrics *metrics.Metrics,
) *Factory {
	return &Factory{
		log:           log.WithModule("client", config.GetLog()),
		clientOptions: config.GetClients(),
		discovery:     discovery,
		tracing:       tracing,
		metrics:       metrics,
	}
}

type clientKey struct {
	name   string
	option *ClientOption
}

func (key clientKey) isGrpc() bool {
	return utils.Includes([]kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_Protocol{
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_GRPC,
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_GRPCS,
	}, key.option.GetProtocol())
}

func (key clientKey) isHttp() bool {
	return utils.Includes([]kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_Protocol{
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_HTTP,
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_HTTPS,
	}, key.option.GetProtocol())
}

func (key clientKey) isSecurity() bool {
	return utils.Includes([]kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_Protocol{
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_GRPCS,
		kratos_foundation_pb.ClientComponentConfig_Client_ClientOption_HTTPS,
	}, key.option.GetProtocol())
}

func (f *Factory) ResolveClient(ctx context.Context) (httpClient HttpClient, grpcClient GrpcClient, err error) {
	key := clientKey{name: ConnNameFromContext(ctx)}
	if key.name == "" {
		err = ErrInvalidClientName
		return
	}
	key.option = f.clientOptions[key.name]

	getCache := func() bool {
		conn, ok := f.conn.Load(key)
		if !ok {
			return false
		}

		if key.isGrpc() {
			grpcClient, ok = conn.(GrpcClient)
			if !ok {
				err = ErrInvalidGrpcClient
			}
			return true
		} else if key.isHttp() {
			httpClient, ok = conn.(HttpClient)
			if !ok {
				err = ErrInvalidHttpClient
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

	f.log.Infof("client initializing, name=%s protocol=%s target=%s", key.name, key.option.GetProtocol().String(), key.option.GetTarget())
	defer func() {
		if err == nil {
			f.log.Infof("client initialized, name=%s", key.name)
		} else {
			f.log.Errorf("client init failed, name=%s err=%v", key.name, err)
		}
	}()

	if key.isGrpc() {
		grpcClient, err = f.newGrpcClient(ctx, key)
		if err != nil {
			return
		}
		f.conn.Store(key, grpcClient)
		return
	} else {
		httpClient, err = f.newHttpClient(ctx, key)
		if err != nil {
			return
		}
		f.conn.Store(key, httpClient)
		return
	}
}
