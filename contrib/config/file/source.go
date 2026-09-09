// Package file 提供按明确优先级组合本地文件的配置源。
package file

import (
	"errors"
	"fmt"
	"path/filepath"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Sources 是供 Wire 区分本地文件配置源集合的独立类型。
type Sources []foundationconfig.Source

// PathList 是文件路径或 filepath.Glob 模式的有序列表；空列表表示禁用文件配置。
type PathList []string

// NewSources 返回按路径解析顺序排列的全部底层文件配置源。
//
// 未匹配路径只记录警告，已存在的空目录仍是有效配置源。
// 由应用把结果交给 config.NewManager；后解析的文件覆盖先解析的文件。
func NewSources(log log.Logger, paths PathList) (Sources, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	matches, unmatched, err := glob(paths...)
	if err != nil {
		return nil, fmt.Errorf("load file source: %w", err)
	}
	for _, pattern := range unmatched {
		log.Warnf("NewSources | glob.unmatched | pattern=%q", pattern)
	}
	if len(matches) == 0 {
		return nil, nil
	}

	log.Infof("NewSources | sources.ready | files=%v", matches)
	sources := make(Sources, len(matches))
	for index, filename := range matches {
		source, err := newFileSource(filename)
		if err != nil {
			return nil, fmt.Errorf("create file source %q: %w", filename, err)
		}
		sources[index] = source
	}
	return sources, nil
}

// glob 按模式顺序返回匹配文件，并去掉重叠模式命中的同一路径。
func glob(patterns ...string) (matches []string, unmatched []string, err error) {
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		if pattern == "" {
			return nil, nil, errors.New("file source pattern is empty")
		}
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"glob file source pattern %q: %w",
				pattern,
				err,
			)
		}
		if len(files) == 0 {
			unmatched = append(unmatched, pattern)
			continue
		}
		for _, filename := range files {
			// 重复加载同一文件不会改变优先级，只会增加解码和监听开销。
			if _, exists := seen[filename]; exists {
				continue
			}
			seen[filename] = struct{}{}
			matches = append(matches, filename)
		}
	}
	return matches, unmatched, nil
}
