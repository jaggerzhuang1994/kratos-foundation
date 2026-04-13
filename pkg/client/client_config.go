// Package internal_client 提供客户端内部实现
package client

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/filter"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"github.com/pkg/errors"
)

type clientConfig struct {
	name   string // 客户端名称
	option interface {
		GetTarget() string
		GetMiddleware() *config_pb.ClientMiddleware
	}
	protocol config_pb.Protocol
}

func newClientConfig(name string, option Option, optionalProtocol ...config_pb.Protocol) clientConfig {
	var protocol = option.GetProtocol() // 默认为 config 中配置的 protocol (配置默认值也是 GRPC)
	if len(optionalProtocol) > 0 {      // 如果调用端指定了协议，则使用具体协议
		protocol = optionalProtocol[0]
	}
	return clientConfig{
		name,
		option,
		protocol,
	}
}

// GetNodeFilters 获取节点过滤器列表，用于服务发现时筛选节点
// 返回的过滤器包括：环境过滤器、协议过滤器、元数据过滤器
func (key *clientConfig) getNodeFilters() ([]selector.NodeFilter, error) {
	// env的节点过滤器
	var nodeFilters = []selector.NodeFilter{
		filter.Env(),
	}
	// 过滤协议
	if key.protocol == config_pb.Protocol_HTTP {
		nodeFilters = append(nodeFilters, filter.HTTP())
	} else if key.protocol == config_pb.Protocol_HTTPS {
		nodeFilters = append(nodeFilters, filter.HTTPS())
	} else if key.protocol == config_pb.Protocol_GRPC {
		nodeFilters = append(nodeFilters, filter.GRPC())
	} else if key.protocol == config_pb.Protocol_GRPCS {
		nodeFilters = append(nodeFilters, filter.GRPCS())
	}
	// target 中的查询参数作为 metadata 过滤器
	// 解析服务发现 url
	targetUrl, err := url.Parse(key.getTarget())
	if err != nil {
		return nil, errors.Wrap(err, ErrParseTargetFailed.Error())
	}
	mdFilter := targetUrl.Query()
	if len(mdFilter) > 0 {
		nodeFilters = append(nodeFilters, filter.MetadataV2(mdFilter))
	}
	return nodeFilters, nil
}

// GetTarget 获取客户端目标地址
// 如果配置中未指定目标，则使用默认的服务发现地址 discovery:///<Name>
func (key *clientConfig) getTarget() string {
	target := key.option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", key.name)
	}
	return target
}

// UseDiscovery 判断是否使用服务发现
func (key *clientConfig) useDiscovery() bool {
	return strings.HasPrefix(key.getTarget(), "discovery://")
}
