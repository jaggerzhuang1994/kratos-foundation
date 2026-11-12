package output

import (
	"fmt"
	"strings"

	"github.com/go-kratos/kratos/v2/log"
)

// NewLevelFilter 创建按最低日志级别过滤的 Logger。
func NewLevelFilter(logger log.Logger, level log.Level) log.Logger {
	if level <= log.LevelDebug {
		return logger
	}
	return log.NewFilter(logger, log.FilterLevel(level))
}

// IsPanicEvent 报告事件名是否表示 panic 被恢复（以 .panicked 或 .panic.recovered 结尾）。
func IsPanicEvent(event string) bool {
	return strings.HasSuffix(event, ".panicked") || strings.HasSuffix(event, ".panic.recovered")
}

// HasPanicEvent 报告一条 ERROR 及以上的日志是否带 panic 事件名。这类日志不受任何级别阈值限制：
// panic 是代码缺陷，调高日志级别不应让它消失。
func HasPanicEvent(level log.Level, keyvals []any) bool {
	if level < log.LevelError {
		return false
	}
	for i := len(keyvals) - 2; i >= 0; i -= 2 {
		if key, ok := keyvals[i].(string); ok && key == "event" {
			return IsPanicEvent(fmt.Sprint(keyvals[i+1]))
		}
	}
	return false
}

type filterLogger struct {
	// logger 字段过滤后的输出目标。
	logger log.Logger
	// filterKeys 精确匹配的过滤字段集合。
	filterKeys map[string]struct{}
	// filterPrefix 按前缀匹配的过滤规则。
	filterPrefix []string
	// filterEmpty 是否移除 nil 或格式化为空字符串的值；不移除数字 0 和 false。
	filterEmpty bool
}

// NewFilter 创建支持空值、精确 key 和尾部 * 前缀规则的过滤 Logger。
func NewFilter(logger log.Logger, filterEmpty bool, filterKeys map[string]struct{}) log.Logger {
	if len(filterKeys) == 0 && !filterEmpty {
		return logger
	}

	exact := make(map[string]struct{}, len(filterKeys))
	prefixes := make([]string, 0, len(filterKeys))
	for key := range filterKeys {
		if before, ok := strings.CutSuffix(key, "*"); ok {
			prefixes = append(prefixes, before)
			continue
		}
		exact[key] = struct{}{}
	}
	return &filterLogger{
		logger:       logger,
		filterKeys:   exact,
		filterPrefix: prefixes,
		filterEmpty:  filterEmpty,
	}
}

// Log 删除敏感字段和空值，并原样返回底层写入错误。
func (f *filterLogger) Log(level log.Level, keyvals ...any) error {
	length := len(keyvals)
	newKeyvals := make([]any, 0, length)

	for i := 0; i < length; i += 2 {
		if key, ok := keyvals[i].(string); ok {
			if f.filtered(key) {
				continue
			}
		}

		if i+1 < length {
			if f.filterEmpty {
				if keyvals[i+1] == nil || fmt.Sprint(keyvals[i+1]) == "" {
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

// filtered 判断键是否命中精确规则或尾部星号转换出的前缀规则。
func (f *filterLogger) filtered(key string) bool {
	// module 是归属契约，任何精确或前缀过滤都必须保留它。
	if key == "module" {
		return false
	}
	if _, ok := f.filterKeys[key]; ok {
		return true
	}
	for _, prefix := range f.filterPrefix {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// FilterKeysSet 将配置切片转换为查询集合，并持有独立快照。
func FilterKeysSet(keys []string) map[string]struct{} {
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}
