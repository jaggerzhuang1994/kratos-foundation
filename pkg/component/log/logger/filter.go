package logger

import (
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
)

func NewFilterLevelLogger(logger log.Logger, level log.Level) log.Logger {
	return log.With(log.NewFilter(logger, log.FilterLevel(level)))
}

type filterKeysLogger struct {
	logger  log.Logger
	filters map[string]struct{}
	// 是否过滤空值
	filterEmpty bool
}

func NewFilterKeysLogger(logger log.Logger, filterEmpty bool, filterKey ...string) log.Logger {
	if len(filterKey) == 0 && !filterEmpty {
		return logger
	}
	filters := make(map[string]struct{}, len(filterKey))
	for _, key := range filterKey {
		filters[key] = struct{}{}
	}

	return &filterKeysLogger{
		logger,
		filters,
		filterEmpty,
	}
}

func (f *filterKeysLogger) Log(level log.Level, keyvals ...any) error {
	length := len(keyvals)
	newKeyvals := make([]any, 0, length)
	for i := 0; i < length; i += 2 {
		if key, ok := keyvals[i].(string); ok {
			if _, ok := f.filters[key]; ok {
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
