package output

import (
	"strings"

	"github.com/go-kratos/kratos/v2/log"
)

// NewModule 保证输出恰好包含一个有效 module；固定模块优先，否则取最后一个有效声明，缺失时为 unknown。
// 字段按 ts、module、caller、其余 KV 排列，不改变其余字段的相对顺序。
func NewModule(logger log.Logger, module string) log.Logger {
	return &moduleLogger{logger: logger, module: module}
}

type moduleLogger struct {
	logger log.Logger
	module string
}

func (l *moduleLogger) Log(level log.Level, keyvals ...any) error {
	module := l.module
	for i := 0; i+1 < len(keyvals); i += 2 {
		if key, ok := keyvals[i].(string); ok && key == "module" && l.module == "" {
			if value, ok := keyvals[i+1].(string); ok && value != "" && strings.TrimSpace(value) == value {
				module = value
			}
		}
	}
	if module == "" {
		module = "unknown"
	}
	values := make([]any, 0, len(keyvals)+2)
	// 只调整展示分组；同名字段仍交给后续过滤和去重，保留原来的覆盖语义。
	for _, group := range []string{"ts", "module", "caller", ""} {
		if group == "module" {
			values = append(values, "module", module)
			continue
		}
		for i := 0; i < len(keyvals); i += 2 {
			key, _ := keyvals[i].(string)
			if group == "" {
				if key == "ts" || key == "module" || key == "caller" {
					continue
				}
			} else if key != group {
				continue
			}
			values = append(values, keyvals[i])
			if i+1 < len(keyvals) {
				values = append(values, keyvals[i+1])
			} else {
				// 补齐孤立字段，避免后面的字段被误认为它的值。
				values = append(values, "(MISSING)")
			}
		}
	}
	return l.logger.Log(level, values...)
}
