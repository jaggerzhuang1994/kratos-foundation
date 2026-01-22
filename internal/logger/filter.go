package internal_logger

import (
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
)

// NewFilterLevelLogger 创建一个按日志级别过滤的日志器
// 如果日志级别为 LevelDebug，则返回原日志器（不过滤）
// 否则包装日志器，只输出不低于指定级别的日志
func NewFilterLevelLogger(logger log.Logger, level log.Level) log.Logger {
	if level == log.LevelDebug {
		return logger
	}
	return log.NewFilter(logger, log.FilterLevel(level))
}

// filterLogger 支持按键和空值过滤的日志器
type filterLogger struct {
	logger log.Logger
	filterKeys map[string]struct{} // 需要过滤的键集合（这些键不会被记录）
	filterEmpty bool               // 是否过滤空值
}

// NewFilterLogger 创建一个支持按键和空值过滤的日志器
// 参数：
//   - logger: 底层日志器
//   - filterEmpty: 是否过滤空值（为空的值不会被记录）
//   - filterKeys: 需要过滤的键集合（这些键不会被记录）
//
// 返回：如果未设置任何过滤条件，返回原日志器；否则返回包装后的过滤日志器
func NewFilterLogger(logger log.Logger, filterEmpty bool, filterKeys map[string]struct{}) log.Logger {
	if len(filterKeys) == 0 && !filterEmpty {
		return logger
	}

	return &filterLogger{
		logger,
		filterKeys,
		filterEmpty,
	}
}

// Log 实现 log.Logger 接口，输出过滤后的日志
// 过滤规则：
//  1. 如果键在 filterKeys 中，则跳过该键值对
//  2. 如果 filterEmpty 为 true 且值为空，则跳过该键值对
//  3. 保留未在过滤列表中的键值对
func (f *filterLogger) Log(level log.Level, keyvals ...any) error {
	length := len(keyvals)
	newKeyvals := make([]any, 0, length)

	// 遍历键值对，应用过滤规则
	for i := 0; i < length; i += 2 {
		// 检查键是否在过滤列表中
		if key, ok := keyvals[i].(string); ok {
			if _, ok := f.filterKeys[key]; ok {
				continue
			}
		}

		// 处理值
		if i+1 <= length-1 {
			// 如果启用空值过滤且值为空，则跳过
			if f.filterEmpty {
				if fmt.Sprintf("%v", keyvals[i+1]) == "" {
					continue
				}
			}
			newKeyvals = append(newKeyvals, keyvals[i])
			newKeyvals = append(newKeyvals, keyvals[i+1])
		} else {
			// 只有键没有值的情况
			newKeyvals = append(newKeyvals, keyvals[i])
		}
	}
	return f.logger.Log(level, newKeyvals...)
}
