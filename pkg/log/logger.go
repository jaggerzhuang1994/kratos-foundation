package log

import (
	"context"
	"fmt"
	"os"
	"sync"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
)

// Logger 在 Kratos Logger 契约上补充常用的派生和 Helper 方法。
// 所有派生方法都返回新对象，不会修改原 Logger。
type Logger interface {
	kratoslog.Logger
	// WithLevel 设置实例最低级别；模块运行期 level 优先，请求 debug 可以临时放宽。
	WithLevel(kratoslog.Level) Logger

	// With 返回附加固定键值的派生 Logger。
	With(...any) Logger
	// WithModule 返回附加固定模块名的派生 Logger；模块名必须是非空常量。
	WithModule(string) Logger
	// WithContext 返回绑定上下文 Valuer 求值环境的派生 Logger。
	WithContext(context.Context) Logger
	// WithCallerDepth 选择过滤内置日志包装后的第 n 个调用点，默认 1；n <= 0 恢复默认值。
	WithCallerDepth(int) Logger
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

// logger 是绑定 sharedState 的不可变业务 Logger。
type logger struct {
	shared *sharedState
	config *configState

	level       *kratoslog.Level
	filterKeys  []string
	kv          []any
	callerDepth int
	ctx         context.Context
	module      string

	mu    sync.RWMutex
	cache *loggerCache
}

// configState 是单个 Wire Logger 持有的不可变默认配置及输出，派生 Logger 复用它。
type configState struct {
	output      kratoslog.Logger
	disabled    bool
	level       kratoslog.Level
	filterEmpty bool
	filterKeys  []string
	timeFormat  string
	msgKey      string
}

// NewLogger 读取 LOG_* 并创建独立输出；cleanup 由对应 Wire 实例持有和释放。
// 派生 Logger 复用该实例输出，配置中心发布的运行期策略由所有实例共享。
func NewLogger() (Logger, func(), error) {
	config, err := newEnvConfig()
	if err != nil {
		return nil, nil, err
	}
	return newLogger(processState, config)
}

// newLogger 构造实例资源；关闭只影响本实例及其派生 Logger。
func newLogger(shared *sharedState, config envConfig) (Logger, func(), error) {
	if config.MsgKey == "" {
		config.MsgKey = defaultMsgKey
	}
	if err := validateConfig(config); err != nil {
		return nil, nil, err
	}
	output, cleanup, err := shared.newOutput(config)
	if err != nil {
		return nil, nil, err
	}
	return &logger{shared: shared, config: &configState{
		output: output, level: config.Level, filterEmpty: config.FilterEmpty,
		filterKeys: append([]string(nil), config.FilterKeys...),
		timeFormat: config.TimeFormat, msgKey: config.MsgKey, disabled: config.Disable,
	}}, cleanup, nil
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

func (l *logger) WithContext(ctx context.Context) Logger {
	next := l.clone()
	next.ctx = ctx
	return next
}

func (l *logger) WithCallerDepth(depth int) Logger {
	next := l.clone()
	if depth <= 0 {
		depth = defaultCallerDepth
	}
	next.callerDepth = depth
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
		shared:      l.shared,
		config:      l.config,
		level:       l.level,
		filterKeys:  append([]string(nil), l.filterKeys...),
		kv:          append([]any(nil), l.kv...),
		callerDepth: l.callerDepth,
		ctx:         l.ctx,
		module:      l.module,
	}
}

// levelEnabled 按请求 debug、模块策略、实例、env 解析；禁用不能被放宽。
func (l *logger) levelEnabled(level kratoslog.Level, custom *customState) bool {
	if l.config.disabled {
		return false
	}
	minimum := l.config.level
	if l.level != nil {
		minimum = *l.level
	}
	if override := custom.policy.matchModule(l.module); override != nil {
		if override.Disable != nil && *override.Disable {
			return false
		}
		if override.Level != nil {
			minimum, _ = parseLevel(*override.Level)
		}
	}
	if l.ctx != nil && request.IsDebug(l.ctx) {
		minimum = kratoslog.LevelDebug
	}

	return level >= minimum
}

// log 在锁外求值用户字段；只有内置输出的最终写入进入共享策略边界。
func (l *logger) log(level kratoslog.Level, withMsgKey bool, keyvals ...any) error {
	custom := l.shared.custom.Load()
	if !l.levelEnabled(level, custom) {
		return nil
	}
	cache := l.currentCache()
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	fields := append([]any(nil), cache.fields...)
	for i := 1; i < len(fields); i += 2 {
		if value, ok := fields[i].(kratoslog.Valuer); ok {
			fields[i] = value(ctx)
		}
	}
	if withMsgKey {
		fields = append(fields, cache.msgKey)
	}
	fields = append(fields, keyvals...)
	owned, internal := l.config.output.(*outputLogger)
	if internal {
		// 内置端最终使用 %s/%v 编码。为用户格式化方法建立延迟缓存，
		// 重试只重新应用策略，不会重复调用可重入的 Valuer 或 Stringer。
		fields = memoizeFields(fields)
	}
	for {
		custom = l.shared.custom.Load()
		if !l.levelEnabled(level, custom) {
			return nil
		}
		cache = l.currentCache()
		if cache.customVersion != custom.version {
			continue
		}
		var event eventFields
		filtered := output.NewFilter(&event, false, cache.filterKeys)
		filtered = output.NewModule(filtered, l.module)
		if err := filtered.Log(level, fields...); err != nil {
			return err
		}
		if internal {
			// 先按 key 过滤，敏感或昂贵字段被拒绝时不触发其格式化方法。
			event = freezeFields(event)
		}
		var ready eventFields
		filtered = output.NewFilter(output.NewDedupe(&ready), l.config.filterEmpty, nil)
		if err := filtered.Log(level, event...); err != nil {
			return err
		}
		if !internal {
			// 外部 Logger 保留原始字段类型和自身生命周期，调用时不持有本包锁。
			return l.config.output.Log(level, ready...)
		}
		l.shared.gate.RLock()
		if l.shared.custom.Load() != custom {
			l.shared.gate.RUnlock()
			continue
		}
		err := owned.Log(level, ready...)
		l.shared.gate.RUnlock()
		return err
	}
}

func (l *logger) Log(level kratoslog.Level, keyvals ...any) error {
	return l.log(level, false, keyvals...)
}

func (l *logger) Debug(a ...any) {
	// 格式化可能调用业务 Stringer；根策略拒绝时应直接结束，避免高频无效工作。
	if !l.levelEnabled(kratoslog.LevelDebug, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelDebug, true, fmt.Sprint(a...))
}

func (l *logger) Debugf(format string, a ...any) {
	if !l.levelEnabled(kratoslog.LevelDebug, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelDebug, true, fmt.Sprintf(format, a...))
}

func (l *logger) Debugw(keyvals ...any) {
	_ = l.log(kratoslog.LevelDebug, false, keyvals...)
}

func (l *logger) Info(a ...any) {
	if !l.levelEnabled(kratoslog.LevelInfo, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelInfo, true, fmt.Sprint(a...))
}

func (l *logger) Infof(format string, a ...any) {
	if !l.levelEnabled(kratoslog.LevelInfo, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelInfo, true, fmt.Sprintf(format, a...))
}

func (l *logger) Infow(keyvals ...any) {
	_ = l.log(kratoslog.LevelInfo, false, keyvals...)
}

func (l *logger) Warn(a ...any) {
	if !l.levelEnabled(kratoslog.LevelWarn, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelWarn, true, fmt.Sprint(a...))
}

func (l *logger) Warnf(format string, a ...any) {
	if !l.levelEnabled(kratoslog.LevelWarn, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelWarn, true, fmt.Sprintf(format, a...))
}

func (l *logger) Warnw(keyvals ...any) {
	_ = l.log(kratoslog.LevelWarn, false, keyvals...)
}

func (l *logger) Error(a ...any) {
	if !l.levelEnabled(kratoslog.LevelError, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelError, true, fmt.Sprint(a...))
}

func (l *logger) Errorf(format string, a ...any) {
	if !l.levelEnabled(kratoslog.LevelError, l.shared.custom.Load()) {
		return
	}
	_ = l.log(kratoslog.LevelError, true, fmt.Sprintf(format, a...))
}

func (l *logger) Errorw(keyvals ...any) {
	_ = l.log(kratoslog.LevelError, false, keyvals...)
}

func (l *logger) Fatal(a ...any) {
	if l.levelEnabled(kratoslog.LevelFatal, l.shared.custom.Load()) {
		_ = l.log(kratoslog.LevelFatal, true, fmt.Sprint(a...))
	}
	os.Exit(1)
}

func (l *logger) Fatalf(format string, a ...any) {
	if l.levelEnabled(kratoslog.LevelFatal, l.shared.custom.Load()) {
		_ = l.log(kratoslog.LevelFatal, true, fmt.Sprintf(format, a...))
	}
	os.Exit(1)
}

func (l *logger) Fatalw(keyvals ...any) {
	_ = l.log(kratoslog.LevelFatal, false, keyvals...)
	os.Exit(1)
}
