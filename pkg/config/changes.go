package config

import (
	"maps"
	"reflect"
	"slices"
	"strings"
)

// changedPaths 返回订阅范围内的绝对 JSON Pointer 路径，不包含配置值。
// 新增、删除、类型变化和数组变化停在该节点；最多记录 32 项，避免大批更新放大日志。
func changedPaths(key string, before any, existed bool, after any, exists bool) ([]string, bool) {
	const limit = 32
	paths := make([]string, 0)
	truncated := false
	escape := func(segment string) string {
		return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
	}
	var prefix strings.Builder
	if key != "" {
		for segment := range strings.SplitSeq(key, ".") {
			prefix.WriteString("/" + escape(segment))
		}
	}
	var walk func(string, any, bool, any, bool)
	walk = func(path string, old any, had bool, next any, has bool) {
		if truncated || had == has && reflect.DeepEqual(old, next) {
			return
		}
		oldMap, oldOK := old.(map[string]any)
		nextMap, nextOK := next.(map[string]any)
		if had && has && oldOK && nextOK {
			keys := slices.Collect(maps.Keys(oldMap))
			for name := range nextMap {
				if _, ok := oldMap[name]; !ok {
					keys = append(keys, name)
				}
			}
			slices.Sort(keys)
			for _, name := range keys {
				oldValue, oldFound := oldMap[name]
				nextValue, nextFound := nextMap[name]
				walk(path+"/"+escape(name), oldValue, oldFound, nextValue, nextFound)
				if truncated {
					break
				}
			}
			return
		}
		if len(paths) == limit {
			truncated = true
			return
		}
		// JSON Pointer 的空路径表示根；用非空展示值避免日志过滤器丢失标记。
		if path == "" {
			path = "<root>"
		}
		paths = append(paths, path)
	}
	walk(prefix.String(), before, existed, after, exists)
	return paths, truncated
}
