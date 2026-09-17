package deadline

import (
	"slices"
	"strings"
)

// prefixIndex 是构造后只读的压缩前缀树，边按字节区分，保持 strings.HasPrefix 语义。
// 单子节点路径压缩成一条边，查询成本取决于 operation 长度而非规则总数。
type prefixIndex struct {
	// root 指向编译后只读的压缩前缀树根节点。
	root *prefixNode
	// emptyOperation 保存原始首条前缀策略，保持空操作名的既有匹配语义。
	emptyOperation policy
}

type prefixNode struct {
	// prefix 保存当前压缩边对应的字符串片段。
	prefix string
	// value 保存当前前缀对应的策略，仅 hasValue 为 true 时有效。
	value policy
	// hasValue 区分策略节点与仅用于分支的中间节点。
	hasValue bool
	// children 按下一个字节索引子节点，编译后只读。
	children map[byte]*prefixNode
}

// newPrefixIndex 只修改本次编译独占的规则切片；保存排序前首条规则的兼容语义。
func newPrefixIndex(routes []routePolicy) *prefixIndex {
	if len(routes) == 0 {
		return nil
	}
	index := &prefixIndex{emptyOperation: routes[0].policy}
	slices.SortFunc(routes, func(a, b routePolicy) int { return strings.Compare(a.prefix, b.prefix) })
	index.root = buildPrefixNode(routes, 0)
	return index
}

func buildPrefixNode(routes []routePolicy, offset int) *prefixNode {
	first, last := routes[0].prefix, routes[len(routes)-1].prefix
	common := offset
	for common < len(first) && common < len(last) && first[common] == last[common] {
		common++
	}
	node := &prefixNode{prefix: first[offset:common]}
	if len(first) == common {
		node.value, node.hasValue = routes[0].policy, true
		routes = routes[1:]
	}
	if len(routes) > 0 {
		node.children = make(map[byte]*prefixNode)
	}
	for start := 0; start < len(routes); {
		edge := routes[start].prefix[common]
		end := start + 1
		for end < len(routes) && routes[end].prefix[common] == edge {
			end++
		}
		node.children[edge] = buildPrefixNode(routes[start:end], common)
		start = end
	}
	return node
}

func (index *prefixIndex) lookup(operation string) (policy, bool) {
	if index == nil {
		return policy{}, false
	}
	// 历史空 operation 命中原始配置中的首个前缀，不能被排序后的首项替代。
	if operation == "" {
		return index.emptyOperation, true
	}
	var selected policy
	found := false
	for node := index.root; node != nil && strings.HasPrefix(operation, node.prefix); {
		operation = operation[len(node.prefix):]
		if node.hasValue {
			selected, found = node.value, true
		}
		if operation == "" {
			break
		}
		node = node.children[operation[0]]
	}
	return selected, found
}
