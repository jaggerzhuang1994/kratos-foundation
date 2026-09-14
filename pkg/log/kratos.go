package log

import (
	"context"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

// SetLogger 借用输出并安装 Kratos 模块适配器；输出资源仍由调用方释放。
func SetLogger(target kratoslog.Logger) {
	if bridge, ok := target.(*kratosBridge); ok {
		target = bridge.target
	}
	kratosTarget := target
	if base, ok := target.(Logger); ok {
		kratosTarget = base.WithModule("kratos")
	}
	kratoslog.SetLogger(&kratosBridge{target: target, logger: output.NewModule(kratosTarget, "kratos")})
}

// GetLogger 返回原始借用的 Logger，供 Foundation 调用及 Bootstrap 恢复绑定。
func GetLogger() kratoslog.Logger {
	target := kratoslog.GetLogger()
	if bridge, ok := target.(*kratosBridge); ok {
		return bridge.target
	}
	return target
}

type kratosBridge struct {
	target kratoslog.Logger
	logger kratoslog.Logger
}

func (l *kratosBridge) Log(level kratoslog.Level, keyvals ...any) error {
	return l.logger.Log(level, keyvals...)
}

type globalLogger struct{}

func (globalLogger) Log(level kratoslog.Level, keyvals ...any) error {
	// 每次读取现有全局绑定，不缓存旧输出，也不新增全局可变状态。
	target := GetLogger()
	if base, ok := target.(*logger); ok {
		return base.Log(level, keyvals...)
	}
	return output.NewModule(target, "").Log(level, keyvals...)
}

var globalHelper = kratoslog.NewHelper(globalLogger{})

// currentLogger 借用当前全局输出；外部 Logger 通过既有实例管线应用共享字段和消息设置。
// 不创建文件或 cleanup，视图的输出生命周期仍由全局绑定的所有者管理。
func currentLogger() *logger {
	target := GetLogger()
	if base, ok := target.(*logger); ok {
		return base
	}
	return &logger{shared: processState, config: &configState{
		output: target, level: kratoslog.LevelDebug,
		filterEmpty: true, timeFormat: time.RFC3339, msgKey: defaultMsgKey,
	}}
}

// WithModule 派生当前全局 Logger 的模块视图，可继续 With 字段并通过 Info/Error 等方法记录消息。
// 视图借用当前输出；后续 SetLogger 不会重绑已保存的视图，应在使用处获取。
func WithModule(module string) Logger { return currentLogger().WithModule(module) }

// Context 返回绑定上下文和当前消息字段名的全局日志 Helper。
// Helper 保存构造时的 msgKey；需要持续跟随共享设置时使用 WithModule(...).WithContext(ctx)。
func Context(ctx context.Context) *kratoslog.Helper {
	base := currentLogger()
	key := base.config.msgKey
	if custom := base.shared.custom.Load(); custom.msgKey != "" {
		key = custom.msgKey
	}
	if base.msgKey != "" {
		key = base.msgKey
	}
	return kratoslog.NewHelper(base.WithContext(ctx), kratoslog.WithMessageKey(key))
}

var Log = globalHelper.Log
var Debugw = globalHelper.Debugw
var Infow = globalHelper.Infow
var Warnw = globalHelper.Warnw
var Errorw = globalHelper.Errorw
var Fatalw = globalHelper.Fatalw

// Debug 使用当前全局 Logger 的消息字段记录调试日志。
func Debug(a ...any) { currentLogger().Debug(a...) }

// Debugf 使用当前全局 Logger 的消息字段记录格式化调试日志。
func Debugf(format string, a ...any) { currentLogger().Debugf(format, a...) }

// Info 使用当前全局 Logger 的消息字段记录信息日志。
func Info(a ...any) { currentLogger().Info(a...) }

// Infof 使用当前全局 Logger 的消息字段记录格式化信息日志。
func Infof(format string, a ...any) { currentLogger().Infof(format, a...) }

// Warn 使用当前全局 Logger 的消息字段记录警告日志。
func Warn(a ...any) { currentLogger().Warn(a...) }

// Warnf 使用当前全局 Logger 的消息字段记录格式化警告日志。
func Warnf(format string, a ...any) { currentLogger().Warnf(format, a...) }

// Error 使用当前全局 Logger 的消息字段记录错误日志。
func Error(a ...any) { currentLogger().Error(a...) }

// Errorf 使用当前全局 Logger 的消息字段记录格式化错误日志。
func Errorf(format string, a ...any) { currentLogger().Errorf(format, a...) }

// Fatal 使用当前全局 Logger 的消息字段记录致命错误并退出进程的日志。
func Fatal(a ...any) { currentLogger().Fatal(a...) }

// Fatalf 使用当前全局 Logger 的消息字段记录格式化致命错误并退出进程的日志。
func Fatalf(format string, a ...any) { currentLogger().Fatalf(format, a...) }

// 未组装应用时只使用标准输出；Bootstrap 安装实例后借用它的输出资源。
func init() {
	fallback := &logger{shared: processState, config: &configState{
		output: &outputLogger{output: output.NewStd()}, level: kratoslog.LevelInfo,
		filterEmpty: true, timeFormat: time.RFC3339, msgKey: defaultMsgKey,
	}}
	SetLogger(fallback)
}
