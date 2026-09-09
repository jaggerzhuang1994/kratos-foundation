package log

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// ModuleConfig 定义模块日志配置所需的最小契约。
type ModuleConfig interface {
	// GetDisable 返回是否禁用该模块日志。
	GetDisable() bool
	// GetLevel 返回该模块覆盖的最低日志级别。
	GetLevel() string
	// GetFilterKeys 返回该模块额外过滤的敏感字段。
	GetFilterKeys() []string
}

// Logger 在 Kratos Logger 契约上补充常用的派生和 Helper 方法。
// 所有派生方法都返回新对象，不会修改原 Logger。
type Logger interface {
	kratoslog.Logger

	// With 返回附加固定键值的派生 Logger。
	With(...any) Logger
	// WithModule 返回附加固定模块名的派生 Logger；模块名必须是非空常量。
	WithModule(string) Logger
	// WithModuleConfig 校验并应用来自配置文件的模块日志策略。
	WithModuleConfig(string, ModuleConfig) (Logger, error)
	// WithContext 返回绑定上下文 Valuer 求值环境的派生 Logger。
	WithContext(context.Context) Logger
	// WithCallerDepth 返回使用绝对调用深度的派生 Logger。
	WithCallerDepth(int) Logger
	// AddCallerDepth 设置相对调用深度增量；无参数时为 1，只使用首个参数，链式调用以后一次为准。
	AddCallerDepth(optionalCallerDepth ...int) Logger
	// WithFilterKeys 返回增加敏感字段过滤规则的派生 Logger。
	WithFilterKeys(...string) Logger

	// Debug 记录 Debug 级别日志。
	Debug(a ...any)
	// Debugf 按格式记录 Debug 级别日志。
	Debugf(format string, a ...any)
	// Debugw 按键值记录 Debug 级别日志。
	Debugw(keyvals ...any)
	// Info 记录 Info 级别日志。
	Info(a ...any)
	// Infof 按格式记录 Info 级别日志。
	Infof(format string, a ...any)
	// Infow 按键值记录 Info 级别日志。
	Infow(keyvals ...any)
	// Warn 记录 Warn 级别日志。
	Warn(a ...any)
	// Warnf 按格式记录 Warn 级别日志。
	Warnf(format string, a ...any)
	// Warnw 按键值记录 Warn 级别日志。
	Warnw(keyvals ...any)
	// Error 记录 Error 级别日志。
	Error(a ...any)
	// Errorf 按格式记录 Error 级别日志。
	Errorf(format string, a ...any)
	// Errorw 按键值记录 Error 级别日志。
	Errorw(keyvals ...any)
	// Fatal 记录 Fatal 级别日志并退出进程。
	Fatal(a ...any)
	// Fatalf 按格式记录 Fatal 级别日志并退出进程。
	Fatalf(format string, a ...any)
	// Fatalw 按键值记录 Fatal 级别日志并退出进程。
	Fatalw(keyvals ...any)
}

// logger 是绑定 SharedState 的不可变业务 Logger。
type logger struct {
	shared *SharedState

	disabled         bool
	level            *kratoslog.Level
	filterEmpty      *bool
	filterKeys       []string
	kv               []any
	callerDepth      int
	callerDepthDelta int
	timeFormat       string
	ctx              context.Context
	module           string
	msgKey           string

	mu    sync.RWMutex
	cache *loggerCache
}

// NewLogger 返回绑定 SharedState 的业务 Logger。
func NewLogger(shared *SharedState) Logger {
	return &logger{shared: shared}
}

func (l *logger) With(kv ...any) Logger {
	next := l.clone()
	next.kv = append(next.kv, kv...)
	return next
}

func (l *logger) WithModule(module string) Logger {
	if err := validateModule(module); err != nil {
		panic(err)
	}
	next := l.clone()
	next.module = module
	return next
}

func (l *logger) WithModuleConfig(module string, config ModuleConfig) (Logger, error) {
	if err := validateModule(module); err != nil {
		return nil, err
	}
	next := l.clone()
	next.module = module
	if config != nil {
		next.disabled = config.GetDisable()
		if value := config.GetLevel(); value != "" {
			level, valid := parseLevel(value)
			if !valid {
				return nil, fmt.Errorf("log module %q level must be one of debug, info, warn, error, fatal", module)
			}
			next.level = &level
		}
		filterKeys := config.GetFilterKeys()
		if err := validateFilterKeys(filterKeys); err != nil {
			return nil, fmt.Errorf("log module %q: %w", module, err)
		}
		next.filterKeys = append(next.filterKeys, filterKeys...)
	}
	return next, nil
}

func (l *logger) WithContext(ctx context.Context) Logger {
	next := l.clone()
	next.ctx = ctx
	return next
}

func (l *logger) WithCallerDepth(callerDepth int) Logger {
	next := l.clone()
	next.callerDepth = callerDepth
	next.callerDepthDelta = 0
	return next
}

// AddCallerDepth 设置相对调用深度增量，而不是在已有增量上累加。
// 无参数时增量为 1；传入多个参数时只使用第一个。
func (l *logger) AddCallerDepth(optionalCallerDepth ...int) Logger {
	next := l.clone()
	callerDepthDelta := 1
	if len(optionalCallerDepth) > 0 {
		callerDepthDelta = optionalCallerDepth[0]
	}
	next.callerDepthDelta = callerDepthDelta
	return next
}

func (l *logger) WithFilterKeys(filterKeys ...string) Logger {
	next := l.clone()
	next.filterKeys = append(next.filterKeys, filterKeys...)
	return next
}

// clone 复制派生配置，但不继承 version cache。
func (l *logger) clone() *logger {
	return &logger{
		shared:           l.shared,
		disabled:         l.disabled,
		level:            l.level,
		filterEmpty:      l.filterEmpty,
		filterKeys:       append([]string(nil), l.filterKeys...),
		kv:               append([]any(nil), l.kv...),
		callerDepth:      l.callerDepth,
		callerDepthDelta: l.callerDepthDelta,
		timeFormat:       l.timeFormat,
		ctx:              l.ctx,
		module:           l.module,
		msgKey:           l.msgKey,
	}
}

// log 在旧输出已关闭且新 Config 版本已发布时重新读取状态并重试。
func (l *logger) log(level kratoslog.Level, withMsgKey bool, keyvals ...any) error {
	if l.disabled {
		return nil
	}
	for {
		config := l.shared.config.Load()
		if config == nil {
			return errSharedStateReleased
		}
		custom := l.shared.custom.Load()

		minimum := config.level
		if l.level != nil {
			minimum = *l.level
		} else if custom.level != nil {
			minimum = *custom.level
		}
		if level < minimum {
			return nil
		}

		if l.expired(config, custom) && !l.buildCache(config, custom) {
			continue
		}

		err := func() error {
			l.mu.RLock()
			defer l.mu.RUnlock()

			writeKeyvals := keyvals
			if withMsgKey {
				writeKeyvals = append([]any{l.cache.msgKey}, keyvals...)
			}
			return l.cache.logger.Log(level, writeKeyvals...)
		}()

		latest := l.shared.config.Load()
		if errors.Is(err, os.ErrClosed) && latest != nil && latest != config {
			continue
		}
		return err
	}
}

func (l *logger) Log(level kratoslog.Level, keyvals ...any) error {
	return l.log(level, false, keyvals...)
}

func (l *logger) Debug(a ...any) {
	_ = l.log(kratoslog.LevelDebug, true, fmt.Sprint(a...))
}

func (l *logger) Debugf(format string, a ...any) {
	_ = l.log(kratoslog.LevelDebug, true, fmt.Sprintf(format, a...))
}

func (l *logger) Debugw(keyvals ...any) {
	_ = l.log(kratoslog.LevelDebug, false, keyvals...)
}

func (l *logger) Info(a ...any) {
	_ = l.log(kratoslog.LevelInfo, true, fmt.Sprint(a...))
}

func (l *logger) Infof(format string, a ...any) {
	_ = l.log(kratoslog.LevelInfo, true, fmt.Sprintf(format, a...))
}

func (l *logger) Infow(keyvals ...any) {
	_ = l.log(kratoslog.LevelInfo, false, keyvals...)
}

func (l *logger) Warn(a ...any) {
	_ = l.log(kratoslog.LevelWarn, true, fmt.Sprint(a...))
}

func (l *logger) Warnf(format string, a ...any) {
	_ = l.log(kratoslog.LevelWarn, true, fmt.Sprintf(format, a...))
}

func (l *logger) Warnw(keyvals ...any) {
	_ = l.log(kratoslog.LevelWarn, false, keyvals...)
}

func (l *logger) Error(a ...any) {
	_ = l.log(kratoslog.LevelError, true, fmt.Sprint(a...))
}

func (l *logger) Errorf(format string, a ...any) {
	_ = l.log(kratoslog.LevelError, true, fmt.Sprintf(format, a...))
}

func (l *logger) Errorw(keyvals ...any) {
	_ = l.log(kratoslog.LevelError, false, keyvals...)
}

func (l *logger) Fatal(a ...any) {
	_ = l.log(kratoslog.LevelFatal, true, fmt.Sprint(a...))
	os.Exit(1)
}

func (l *logger) Fatalf(format string, a ...any) {
	_ = l.log(kratoslog.LevelFatal, true, fmt.Sprintf(format, a...))
	os.Exit(1)
}

func (l *logger) Fatalw(keyvals ...any) {
	_ = l.log(kratoslog.LevelFatal, false, keyvals...)
	os.Exit(1)
}
