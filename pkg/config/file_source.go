package config

import (
	"path/filepath"

	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/pkg/errors"
)

type FileSource Source
type FileSourcePathList []string

func NewFileSource(
	log log.Log,
	fileSourcePathList FileSourcePathList,
) (FileSource, error) {
	if len(fileSourcePathList) == 0 {
		log.Info("not load file source: file config path is empty")
		return nil, nil
	}
	matches, err := glob(fileSourcePathList...)
	if err != nil {
		return nil, errors.WithMessage(err, "load file source failed")
	}
	log.Info("file config list ", matches)
	// 通配符返回的文件列表构成一个优先级组，优先级按照glob返回的顺序
	return NewPriorityConfigSource(utils.Map(matches, func(filename string) config.Source {
		return file.NewSource(filename)
	})), nil
}

func glob(patterns ...string) ([]string, error) {
	var matches []string
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, errors.WithMessagef(err, "glob %s failed", pattern)
		}
		matches = append(matches, files...)
	}
	return matches, nil
}
