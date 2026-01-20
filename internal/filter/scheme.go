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

// GRPC 协议
func GRPC() selector.NodeFilter {
	return Scheme(schemeGrpc)
}

// HTTP 协议
func HTTP() selector.NodeFilter {
	return Scheme(schemeHttp)
}

// GRPCS 协议
func GRPCS() selector.NodeFilter {
	return Scheme(schemeGrpcs)
}

// HTTPS 协议
func HTTPS() selector.NodeFilter {
	return Scheme(schemeHttps)
}
