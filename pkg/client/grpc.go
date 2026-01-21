package client

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport/grpc"
)

func (f *factory) newGRPCClient(
	ctx context.Context,
	clientConfig ClientConfig,
) (GRPCClient, error) {
	var opts []grpc.ClientOption

	// 访问端点配置
	endpointOptions, err := f.getGRPCEndpointOption(clientConfig)
	if err != nil {
		return nil, err
	}
	opts = append(opts, endpointOptions...)

	// 显式设置 timeout = 0，使用 timeout 中间件控制超时
	opts = append(opts, grpc.WithTimeout(0))

	// 中间件
	opts = append(opts, grpc.WithMiddleware(f.newMiddleware(clientConfig)...))

	conn, err := grpc.DialInsecure(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (f *factory) getGRPCEndpointOption(clientConfig ClientConfig) ([]grpc.ClientOption, error) {
	target := clientConfig.GetTarget()

	if !clientConfig.UseDiscovery() {
		// 使用直连
		return []grpc.ClientOption{
			grpc.WithEndpoint(target),
		}, nil
	}

	// 获取服务发现组件
	if f.discovery == nil {
		return nil, ErrDiscoveryNotInitialized
	}

	// node 过滤器
	nodeFilters, err := clientConfig.GetNodeFilters()
	if err != nil {
		return nil, err
	}

	return []grpc.ClientOption{
		grpc.WithEndpoint(target),
		grpc.WithDiscovery(f.discovery),
		grpc.WithNodeFilter(nodeFilters...),
	}, nil
}
