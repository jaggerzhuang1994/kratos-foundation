package log

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

var globalBinding atomic.Pointer[kratosBridge]

type kratosProxy struct{}

func (kratosProxy) Log(level kratoslog.Level, keyvals ...any) error {
	// 官方 Config 的这两类错误会拼入整份配置，包括已展开的凭据。
	// 在固定桥接入口替换完整消息，不能保留也可能包含敏感值的 err/key。
	for i := 0; i+1 < len(keyvals); i += 2 {
		key, _ := keyvals[i].(string)
		message, ok := keyvals[i+1].(string)
		if key != "msg" || !ok {
			continue
		}
		for _, prefix := range []string{"Failed to config decode error:", "Failed to config merge error:", "failed to merge config source:", "failed to merge next config:"} {
			if strings.HasPrefix(message, prefix) {
				keyvals = slices.Clone(keyvals)
				keyvals[i+1] = "Failed to load configuration; raw configuration error omitted"
				break
			}
		}
	}
	return globalBinding.Load().logger.Log(level, keyvals...)
}

// SetLogger 原子切换借用的全局输出，返回幂等恢复函数；输出仍由调用方释放。
// 恢复仅在此绑定仍为当前绑定时生效，不覆盖后来安装的 Logger。
// 应使用本入口，不能在运行期直接调用非线程安全的 kratoslog.SetLogger。
func SetLogger(target kratoslog.Logger) func() {
	if _, ok := target.(kratosProxy); ok {
		target = GetLogger()
	}
	kratosTarget := target
	if base, ok := target.(Logger); ok {
		kratosTarget = base.WithModule("kratos")
	}
	next := &kratosBridge{target: target, logger: output.NewModule(kratosTarget, "kratos")}
	previous := globalBinding.Swap(next)
	var restored atomic.Bool
	return func() {
		if restored.CompareAndSwap(false, true) {
			globalBinding.CompareAndSwap(next, previous)
		}
	}
}

// GetLogger 返回当前原始借用 Logger；不会暴露固定安装的 Kratos 代理。
func GetLogger() kratoslog.Logger { return globalBinding.Load().target }

type kratosBridge struct {
	// target 调用方提供的原始 Logger，仅借用而不接管释放。
	target kratoslog.Logger
	// logger 附加 Kratos 模块身份后的实际输出入口。
	logger kratoslog.Logger
}

type globalLogger struct{}

func (globalLogger) Log(level kratoslog.Level, keyvals ...any) error {
	return currentLogger().Log(level, keyvals...)
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

// Context 返回绑定上下文和当前消息字段名的全局日志 Helper。
// Helper 保存构造时的 msgKey；需要持续跟随共享设置时使用 WithModule(...).WithContext(ctx)。
func Context(ctx context.Context) *kratoslog.Helper {
	base := currentLogger()
	key := base.config.msgKey
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
	std := output.NewStd()
	fallback := &logger{shared: processState, config: &configState{
		output: &outputLogger{preparedOutput: &preparedOutput{output: std, config: envConfig{Std: outputConfig{Level: kratoslog.LevelDebug}}}}, level: kratoslog.LevelInfo,
		filterEmpty: true, timeFormat: time.RFC3339, msgKey: defaultMsgKey,
	}}
	SetLogger(fallback)
	// 包初始化在启动 goroutine 前执行；此后不再改写官方全局 Logger。
	kratoslog.SetLogger(kratosProxy{})
}
