package config

import (
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// changedPaths 返回订阅范围内发生变化的点分路径（如 server.http.addr），不包含配置值。
// 含点号或为空的段使用 ["段"] 表示；新增、删除、类型变化和数组变化停在该节点；
// 最多记录 32 项，避免大批更新放大日志。根节点变化记为 <root>。
func changedPaths(key string, before any, existed bool, after any, exists bool) ([]string, bool) {
	const limit = 32
	paths := make([]string, 0)
	truncated := false
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
				walk(joinPath(path, name), oldValue, oldFound, nextValue, nextFound)
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
		// 空路径表示根；用非空展示值避免日志过滤器丢失标记。
		if path == "" {
			path = "<root>"
		}
		paths = append(paths, path)
	}
	walk(key, before, existed, after, exists)
	return paths, truncated
}

// joinPath 以点号拼接配置路径段，含点号或为空的段加引号，保证路径可无歧义还原。
func joinPath(prefix, segment string) string {
	if segment == "" || strings.Contains(segment, ".") {
		return prefix + "[" + strconv.Quote(segment) + "]"
	}
	if prefix == "" {
		return segment
	}
	return prefix + "." + segment
}
