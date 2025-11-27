package filter

import (
	"context"

	"github.com/go-kratos/kratos/v2/selector"
)

const (
	schemeGrpc  = "grpc"
	schemeHttp  = "http"
	schemeGrpcs = "grpcs"
	schemeHttps = "https"
)

// Scheme is scheme filter.
func Scheme(scheme string) selector.NodeFilter {
	return func(_ context.Context, nodes []selector.Node) []selector.Node {
		newNodes := make([]selector.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.Scheme() == scheme {
				newNodes = append(newNodes, n)
			}
		}
		return newNodes
	}
}

// Grpc 协议
func Grpc() selector.NodeFilter {
	return Scheme(schemeGrpc)
}

// Http 协议
func Http() selector.NodeFilter {
	return Scheme(schemeHttp)
}

// Grpcs 协议
func Grpcs() selector.NodeFilter {
	return Scheme(schemeGrpcs)
}

// Https 协议
func Https() selector.NodeFilter {
	return Scheme(schemeHttps)
}
