package client

import (
	"context"
	"fmt"
	"maps"
	"net/url"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

const (
	schemeGRPC  = "grpc"
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func (b *builder) getNodeFilters(s clientSpec) ([]selector.NodeFilter, error) {
	filters := []selector.NodeFilter{environmentNodeFilter(b.environment, b.hostname)}
	switch s.protocol {
	case config_pb.Protocol_GRPC:
		filters = append(filters, schemeNodeFilter(schemeGRPC))
	case config_pb.Protocol_HTTP:
		filters = append(filters, schemeNodeFilter(schemeHTTP))
	case config_pb.Protocol_HTTPS:
		// Kratos HTTP resolver 会为 HTTPS 端点生成 http scheme 的节点。
		filters = append(filters, schemeNodeFilter(schemeHTTP))
	}
	targetURL, err := url.Parse(s.target)
	if err != nil {
		return nil, fmt.Errorf("parse client target %q: %w", s.target, err)
	}
	if metadata := targetURL.Query(); len(metadata) > 0 {
		filters = append(filters, metadataValuesNodeFilter(metadata))
	}
	return filters, nil
}

func allNodes(_ context.Context, nodes []selector.Node) []selector.Node {
	return nodes
}

func selectFirstOrAll(filters ...selector.NodeFilter) selector.NodeFilter {
	return func(ctx context.Context, nodes []selector.Node) []selector.Node {
		for _, filter := range filters {
			if selected := filter(ctx, nodes); len(selected) > 0 {
				return selected
			}
		}
		return nodes
	}
}

func metadataNodeFilter(metadata map[string]string) selector.NodeFilter {
	if len(metadata) == 0 {
		return allNodes
	}
	metadata = maps.Clone(metadata)
	return func(_ context.Context, nodes []selector.Node) []selector.Node {
		selected := make([]selector.Node, 0, len(nodes))
		for _, node := range nodes {
			nodeMetadata := node.Metadata()
			matched := nodeMetadata != nil
			for key, expected := range metadata {
				if nodeMetadata[key] != expected {
					matched = false
					break
				}
			}
			if matched {
				selected = append(selected, node)
			}
		}
		return selected
	}
}

func metadataValuesNodeFilter(metadata url.Values) selector.NodeFilter {
	values := make(map[string]string, len(metadata))
	for key, candidates := range metadata {
		if len(candidates) > 0 {
			values[key] = candidates[0]
		}
	}
	return metadataNodeFilter(values)
}

func schemeNodeFilter(scheme string) selector.NodeFilter {
	return func(_ context.Context, nodes []selector.Node) []selector.Node {
		selected := make([]selector.Node, 0, len(nodes))
		for _, node := range nodes {
			if node.Scheme() == scheme {
				selected = append(selected, node)
			}
		}
		return selected
	}
}

// environmentNodeFilter 使用构造时固定的应用身份，本地环境依次优先同主机、同环境和全部节点。
func environmentNodeFilter(environment, hostname string) selector.NodeFilter {
	if environment == env.Local {
		return selectFirstOrAll(
			metadataNodeFilter(map[string]string{
				appinfo.MetadataEnvironment: environment,
				appinfo.MetadataHostname:    hostname,
			}),
			metadataNodeFilter(map[string]string{appinfo.MetadataEnvironment: environment}),
		)
	}
	return metadataNodeFilter(map[string]string{appinfo.MetadataEnvironment: environment})
}
