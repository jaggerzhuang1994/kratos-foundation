package output

import (
	"strings"

	"github.com/go-kratos/kratos/v2/log"
)

// NewModule 保证输出恰好包含一个有效 module；固定模块优先，否则取最后一个有效声明，缺失时为 unknown。
func NewModule(logger log.Logger, module string) log.Logger {
	return &moduleLogger{logger: logger, module: module}
}

type moduleLogger struct {
	logger log.Logger
	module string
}

func (l *moduleLogger) Log(level log.Level, keyvals ...any) error {
	module := l.module
	values := make([]any, 0, len(keyvals)+2)
	for i := 0; i < len(keyvals); i += 2 {
		key, _ := keyvals[i].(string)
		if key == "module" {
			if l.module == "" && i+1 < len(keyvals) {
				if value, ok := keyvals[i+1].(string); ok && value != "" && strings.TrimSpace(value) == value {
					module = value
				}
			}
			continue
		}
		values = append(values, keyvals[i])
		if i+1 < len(keyvals) {
			values = append(values, keyvals[i+1])
		} else {
			// 补齐孤立字段，避免它将追加的 module 错当作自己的值。
			values = append(values, "(MISSING)")
		}
	}
	if module == "" {
		module = "unknown"
	}
	values = append(values, "module", module)
	return l.logger.Log(level, values...)
}
