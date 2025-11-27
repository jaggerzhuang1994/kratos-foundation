package filter

import (
	"context"
	"net/url"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

// Metadata 按照md过滤，每个key之间的&关系
func Metadata(mdFilter map[string]string) selector.NodeFilter {
	if len(mdFilter) == 0 {
		return All
	}
	return func(ctx context.Context, nodes []selector.Node) []selector.Node {
		newNodes := make([]selector.Node, 0, len(nodes))
		for _, n := range nodes {
			nodeMd := n.Metadata()
			if nodeMd == nil {
				continue
			}
			matched := func() bool {
				for mdKey := range mdFilter {
					if nodeMd[mdKey] != mdFilter[mdKey] {
						return false
					}
				}
				return true
			}()
			if matched {
				newNodes = append(newNodes, n)
			}
		}
		return newNodes
	}
}

// MetadataV2 根据url.Query()过滤md
func MetadataV2(mdFilter url.Values) selector.NodeFilter {
	if len(mdFilter) == 0 {
		return All
	}
	mdMap := utils.MMap(mdFilter, func(vs []string) string {
		if len(vs) == 0 {
			return ""
		}
		return vs[0]
	})
	return Metadata(mdMap)
}
