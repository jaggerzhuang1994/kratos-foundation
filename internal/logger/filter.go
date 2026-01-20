package internal_logger

import (
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
)

func NewFilterLevelLogger(logger log.Logger, level log.Level) log.Logger {
	if level == log.LevelDebug {
		return logger
	}
	return log.NewFilter(logger, log.FilterLevel(level))
}

type filterLogger struct {
	logger log.Logger
	// 过滤哪些 key
	filterKeys map[string]struct{}
	// 过滤空值
	filterEmpty bool
}

// NewFilterLogger
//
//	filterLevel: < filterLevel 不会记录
//	filterEmpty: 如果值为空，则不记录
//	filterKeys: 过滤的keys 不会记录
func NewFilterLogger(logger log.Logger, filterEmpty bool, filterKeys []string) log.Logger {
	if len(filterKeys) == 0 && !filterEmpty {
		return logger
	}

	filters := make(map[string]struct{}, len(filterKeys))
	for _, key := range filterKeys {
		filters[key] = struct{}{}
	}

	return &filterLogger{
		logger,
		filters,
		filterEmpty,
	}
}

func (f *filterLogger) Log(level log.Level, keyvals ...any) error {
	length := len(keyvals)
	newKeyvals := make([]any, 0, length)
	for i := 0; i < length; i += 2 {
		if key, ok := keyvals[i].(string); ok {
			if _, ok := f.filterKeys[key]; ok {
				continue
			}
		}
		if i+1 <= length-1 {
			if f.filterEmpty {
				if fmt.Sprintf("%v", keyvals[i+1]) == "" {
					continue
				}
			}
			newKeyvals = append(newKeyvals, keyvals[i])
			newKeyvals = append(newKeyvals, keyvals[i+1])
		} else {
			newKeyvals = append(newKeyvals, keyvals[i])
		}
	}
	return f.logger.Log(level, newKeyvals...)
}
