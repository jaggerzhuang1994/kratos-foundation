package snapshot

import (
	"strings"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

// Snapshot 保存由有序输入构建的不可变有效配置。
type Snapshot struct {
	values map[string]any
}

// New 在解析前替换环境变量，并按输入顺序合并配置值。
func New(values []*kratosconfig.KeyValue) (*Snapshot, error) {
	tree, err := build(values)
	if err != nil {
		return nil, err
	}
	return &Snapshot{values: tree}, nil
}

// Lookup 按点分路径读取配置值。
func (s *Snapshot) Lookup(path string) (any, bool) {
	if s == nil {
		return nil, false
	}
	return lookup(s.values, path)
}

func lookup(values map[string]any, path string) (any, bool) {
	if path == "" {
		return values, true
	}
	next := values
	keys := strings.Split(path, ".")
	for index, key := range keys {
		value, ok := next[key]
		if !ok {
			return nil, false
		}
		if index == len(keys)-1 {
			return value, true
		}
		nested, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		next = nested
	}
	return nil, false
}
