// Package internal_client 提供客户端内部实现
package client

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/filter"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb/config_pb"
	"github.com/pkg/errors"
)

// ClientConfig 客户端键结构体，用于标识和配置客户端
type ClientConfig struct {
	Name   string // 客户端名称
	Option Option // 客户端配置选项
}

// IsGRPC 判断是否为 gRPC 协议（包括 GRPC 和 GRPCS）
func (key *ClientConfig) IsGRPC() bool {
	return utils.Includes([]config_pb.Protocol{
		config_pb.Protocol_GRPC,
		config_pb.Protocol_GRPCS,
	}, key.Option.GetProtocol())
}

// IsHTTP 判断是否为 HTTP 协议（包括 HTTP 和 HTTPS）
func (key *ClientConfig) IsHTTP() bool {
	return utils.Includes([]config_pb.Protocol{
		config_pb.Protocol_HTTP,
		config_pb.Protocol_HTTPS,
	}, key.Option.GetProtocol())
}

// IsSecurity 判断是否为安全协议（使用 TLS/SSL）
func (key *ClientConfig) IsSecurity() bool {
	return utils.Includes([]config_pb.Protocol{
		config_pb.Protocol_GRPCS,
		config_pb.Protocol_HTTPS,
	}, key.Option.GetProtocol())
}

// GetNodeFilters 获取节点过滤器列表，用于服务发现时筛选节点
// 返回的过滤器包括：环境过滤器、协议过滤器、元数据过滤器
func (key *ClientConfig) GetNodeFilters() ([]selector.NodeFilter, error) {
	// 默认 env 和 协议的过滤器
	var nodeFilters = append([]selector.NodeFilter{
		filter.Env(),
	}, key.ProtocolFilters()...)

	// target 中的查询参数作为 metadata 过滤器
	// 解析服务发现 url
	targetUrl, err := url.Parse(key.GetTarget())
	if err != nil {
		return nil, errors.Wrap(err, ErrParseTargetFailed.Error())
	}
	mdFilter := targetUrl.Query()
	if len(mdFilter) > 0 {
		nodeFilters = append(nodeFilters, filter.MetadataV2(mdFilter))
	}

	return nodeFilters, nil
}

// ProtocolFilters 根据协议类型返回对应的节点过滤器
func (key *ClientConfig) ProtocolFilters() []selector.NodeFilter {
	if key.IsHTTP() {
		if key.IsSecurity() {
			return []selector.NodeFilter{
				filter.HTTPS(),
			}
		}

		return []selector.NodeFilter{
			filter.HTTP(),
		}
	} else if key.IsGRPC() {
		if key.IsSecurity() {
			return []selector.NodeFilter{
				filter.GRPCS(),
			}
		}

		return []selector.NodeFilter{
			filter.GRPC(),
		}
	}

	return nil
}

// GetTarget 获取客户端目标地址
// 如果配置中未指定目标，则使用默认的服务发现地址 discovery:///<Name>
func (key *ClientConfig) GetTarget() string {
	target := key.Option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", key.Name)
	}
	return target
}

// UseDiscovery 判断是否使用服务发现
func (key *ClientConfig) UseDiscovery() bool {
	return strings.HasPrefix(key.GetTarget(), "discovery://")
}
