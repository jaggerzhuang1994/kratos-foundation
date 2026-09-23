// Package file 提供按明确优先级组合本地文件的配置源。
package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Sources 是供 Wire 区分本地文件配置源集合的独立类型。
type Sources []foundationconfig.Source

// PathList 是目录、文件路径或 filepath.Glob 模式的有序列表；已存在的字面路径优先，空列表表示禁用文件配置。
type PathList []string

// NewSources 返回按路径解析顺序排列的全部底层文件配置源。
//
// 使用全局日志记录构造事件，无需先构造应用 Logger。
// 目录在构造时展开为直属 *.yaml 普通文件；glob 也只保留普通文件。
// 未匹配路径（包括空目录）只记录警告，不创建配置源。
// 由应用把结果交给 config.NewManager；后解析的文件覆盖先解析的文件。
func NewSources(paths PathList) (Sources, error) {
	logger := log.WithModule("config/file")
	if len(paths) == 0 {
		return nil, nil
	}

	matches, unmatched, err := glob(paths...)
	if err != nil {
		return nil, fmt.Errorf("load file source: %w", err)
	}
	for _, pattern := range unmatched {
		logger.With("pattern", pattern).Warn("no local configuration files matched the pattern")
	}
	if len(matches) == 0 {
		return nil, nil
	}

	logger.With("files", matches).Info("matched local configuration files")
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
		files, err := resolvePath(pattern)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve file source path %q: %w", pattern, err)
		}
		if len(files) == 0 {
			unmatched = append(unmatched, pattern)
			continue
		}
		for _, filename := range files {
			// 保留同一路径第一次出现的位置，避免重叠模式重复建立配置源和监听。
			if _, exists := seen[filename]; exists {
				continue
			}
			seen[filename] = struct{}{}
			matches = append(matches, filename)
		}
	}
	return matches, unmatched, nil
}

// resolvePath 优先识别字面路径，只在路径不存在时展开 glob；目录不递归。
func resolvePath(location string) ([]string, error) {
	if location == "" {
		return nil, errors.New("file source path is empty")
	}
	info, err := os.Stat(location)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat config path: %w", err)
	}
	var candidates []string
	switch {
	case errors.Is(err, os.ErrNotExist):
		candidates, err = filepath.Glob(location)
		if err != nil {
			return nil, fmt.Errorf("glob config path: %w", err)
		}
	case info.IsDir():
		entries, err := os.ReadDir(location)
		if err != nil {
			return nil, fmt.Errorf("read config directory: %w", err)
		}
		// ReadDir 按名称排序；目录名包含元字符时也保持字面含义。
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".yaml") {
				candidates = append(candidates, filepath.Join(location, entry.Name()))
			}
		}
	case info.Mode().IsRegular():
		return []string{location}, nil
	default:
		return nil, errors.New("config path is not a regular file")
	}
	var files []string
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			return nil, fmt.Errorf("stat matched config file: %w", err)
		}
		// 防止 glob 命中目录后由底层源隐式加载目录内容；允许指向普通文件的符号链接。
		if info.Mode().IsRegular() {
			files = append(files, candidate)
		}
	}
	return files, nil
}
