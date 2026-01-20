package filter

import (
	"context"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/go-kratos/kratos/v2/selector/filter"
)

var Version = filter.Version

var All = func(_ context.Context, nodes []selector.Node) []selector.Node {
	return nodes
}

// SelectFirstOrFallbackAll 按照filters顺序过滤，直到过滤到>0个node
func SelectFirstOrFallbackAll(filters ...selector.NodeFilter) selector.NodeFilter {
	return func(ctx context.Context, nodes []selector.Node) []selector.Node {
		for _, nodeFilter := range filters {
			newNodes := nodeFilter(ctx, nodes)
			if len(newNodes) > 0 {
				return newNodes
			}
		}
		// 都没有过滤node，则fallback返回全部nodes
		return nodes
	}
}
