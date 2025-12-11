package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"

	"path/filepath"
)

// NewFilePatternSource 不同patterns的优先级按照传入的顺序，相同pattern的优先级按照 filepath.Glob 返回的顺序的优先级
func NewFilePatternSource(patterns []string) (config.Source, error) {
	if len(patterns) == 0 {
		log.Warn("file config source is nil")
		return nil, nil
	}
	var matches []string
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		matches = append(matches, files...)
	}
	log.Debug("file config source list ", matches)
	// 通配符返回的文件列表构成一个优先级组，优先级按照glob返回的顺序
	return NewPriorityConfigSource(utils.Map(matches, func(filename string) config.Source {
		return file.NewSource(filename)
	})), nil
}
