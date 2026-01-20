package client

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/transport"
)

func (f *factory) newHTTPClient(
	ctx context.Context,
	clientConfig ClientConfig,
) (HTTPClient, error) {
	var opts []http.ClientOption

	// 访问端点配置
	endpointOptions, err := f.getHTTPEndpointOption(clientConfig)
	if err != nil {
		return nil, err
	}
	opts = append(opts, endpointOptions...)

	// 显式设置 timeout = 0，使用 timeout 中间件控制超时
	opts = append(opts, http.WithTimeout(0))

	// 中间件
	opts = append(opts, http.WithMiddleware(f.newMiddleware(clientConfig)...))

	// 错误反序列化
	opts = append(opts, http.WithErrorDecoder(transport.HttpErrorDecoder()))

	conn, err := http.NewClient(
		ctx, opts...,
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (f *factory) getHTTPEndpointOption(key ClientConfig) ([]http.ClientOption, error) {
	target := key.GetTarget()

	if !key.UseDiscovery() {
		// 使用直连
		return []http.ClientOption{
			http.WithEndpoint(target),
		}, nil
	}

	if f.discovery == nil {
		return nil, ErrDiscoveryNotInitialized
	}

	// node 过滤器
	nodeFilters, err := key.GetNodeFilters()
	if err != nil {
		return nil, err
	}

	return []http.ClientOption{
		http.WithEndpoint(target),
		http.WithDiscovery(f.discovery),
		http.WithNodeFilter(nodeFilters...),
	}, nil
}
