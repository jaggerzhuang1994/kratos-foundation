package log

import (
	"context"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// WithLevel 设置派生 Logger 的实例级别；模块策略和请求 debug 优先，不修改全局策略。
func WithLevel(level kratoslog.Level) Logger { return currentLogger().WithLevel(level) }

// WithFilterKeys 返回追加敏感字段过滤的派生 Logger。
func WithFilterKeys(keys ...string) Logger { return currentLogger().WithFilterKeys(keys...) }

// With 返回附加实例字段的派生 Logger。
func With(values ...any) Logger { return currentLogger().With(values...) }

// WithContext 返回携带请求字段和 debug 标记的派生 Logger。
func WithContext(ctx context.Context) Logger { return currentLogger().WithContext(ctx) }

// WithModule 派生当前全局 Logger 的模块视图，可继续 With 字段并通过 Info/Error 等方法记录消息。
// 视图借用当前输出；后续 SetLogger 不会重绑已保存的视图，应在使用处获取。
func WithModule(module string) Logger { return currentLogger().WithModule(module) }

func (l *logger) WithLevel(level kratoslog.Level) Logger {
	if level < kratoslog.LevelDebug || level > kratoslog.LevelFatal {
		panic("log level is invalid")
	}
	next := l.clone()
	next.level = &level
	return next
}
