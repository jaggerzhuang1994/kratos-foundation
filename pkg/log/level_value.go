package log

import (
	"context"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
)

// levelValue 延迟保存只对一个日志事件级别可见的字段值。
type levelValue struct {
	level             kratoslog.Level
	value             any
	allowRequestDebug bool
}

// AtLevel 包装只在指定事件级别出现的字段值。
// 其他级别会得到 nil；启用 FilterEmpty 时，对应键值会被一起移除。
func AtLevel(level kratoslog.Level, value any) any {
	return levelValue{level: level, value: value}
}

// DebugOnly 包装只在 Debug 事件或请求级 debug 中出现的字段值。
func DebugOnly(value any) any {
	return levelValue{level: kratoslog.LevelDebug, value: value, allowRequestDebug: true}
}

// resolve 先判断级别，再求值内部 Valuer，避免非目标级别执行昂贵计算。
func (v levelValue) resolve(ctx context.Context, level kratoslog.Level) any {
	if level != v.level && (!v.allowRequestDebug || !request.IsDebug(ctx)) {
		return nil
	}
	if value, ok := v.value.(kratoslog.Valuer); ok {
		return value(ctx)
	}
	return v.value
}
