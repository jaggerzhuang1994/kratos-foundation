package output

import "github.com/go-kratos/kratos/v2/log"

type dedupeLogger struct {
	logger log.Logger
}

// NewDedupe 创建对重复字符串 key 应用 last-wins 规则的 Logger。
func NewDedupe(logger log.Logger) log.Logger {
	return &dedupeLogger{logger: logger}
}

func (l *dedupeLogger) Log(level log.Level, keyvals ...any) error {
	if !hasDuplicateStringKey(keyvals) {
		return l.logger.Log(level, keyvals...)
	}

	result := make([]any, 0, len(keyvals))
	positions := make(map[string]int, len(keyvals)/2)
	for index := 0; index < len(keyvals); index += 2 {
		if index+1 == len(keyvals) {
			result = append(result, keyvals[index])
			break
		}
		key, ok := keyvals[index].(string)
		if !ok {
			result = append(result, keyvals[index], keyvals[index+1])
			continue
		}
		if position, exists := positions[key]; exists {
			result[position+1] = keyvals[index+1]
			continue
		}
		positions[key] = len(result)
		result = append(result, key, keyvals[index+1])
	}
	return l.logger.Log(level, result...)
}

func hasDuplicateStringKey(keyvals []any) bool {
	for index := 0; index+1 < len(keyvals); index += 2 {
		key, ok := keyvals[index].(string)
		if !ok {
			continue
		}
		for next := index + 2; next+1 < len(keyvals); next += 2 {
			if key == keyvals[next] {
				return true
			}
		}
	}
	return false
}
