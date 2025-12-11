package log

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log/logger"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

type Log struct {
	*Helper
	inner       log.Logger
	presetKv    []any
	level       log.Level
	filterKeys  []string
	filterOpts  []log.FilterOption
	withKv      []any
	withCtx     context.Context
	callerDepth int32
	filterEmpty bool
	module      string
}

const defaultCallerDepth = 4

type logCtxKey struct{}

func NewContext(ctx context.Context, log *Log) context.Context {
	return context.WithValue(ctx, logCtxKey{}, log)
}

func FromContext(ctx context.Context) (log *Log, ok bool) {
	log, ok = ctx.Value(logCtxKey{}).(*Log)
	return
}

func NewLog(cfg *Config) (*Log, func(), error) {
	var loggers []log.Logger
	var rcs []func()

	var filterEmpty = cfg.GetFilterEmpty()

	// 标准输出流日志
	if stdLoggerCfg := cfg.GetStd(); !stdLoggerCfg.GetDisable() {
		stdLogger := logger.NewStdLogger()
		filterKeys := append(cfg.GetFilterKeys(), stdLoggerCfg.GetFilterKeys()...)
		stdFilterEmpty := filterEmpty
		if stdLoggerCfg != nil && stdLoggerCfg.FilterEmpty != nil {
			stdFilterEmpty = *stdLoggerCfg.FilterEmpty
		}
		if len(filterKeys) > 0 || stdFilterEmpty {
			stdLogger = logger.NewFilterKeysLogger(stdLogger, stdFilterEmpty, filterKeys...)
		}
		// 如果指定了 std.level，否则依赖外部的 level
		if stdLoggerCfg != nil && stdLoggerCfg.Level != nil {
			stdLogger = logger.NewFilterLevelLogger(stdLogger, log.ParseLevel(stdLoggerCfg.GetLevel()))
		}
		loggers = append(loggers, stdLogger)
	}

	// 文件日志
	if fileLoggerCfg := cfg.GetFile(); !fileLoggerCfg.GetDisable() {
		// 内部文件日志配置
		interCfg := &logger.FileLoggerConfig{
			Level:    log.ParseLevel(utils.Select(fileLoggerCfg.GetLevel(), cfg.GetLevel(), defaultConfig.GetLevel())),
			Path:     fileLoggerCfg.GetPath(),
			Rotating: nil,
		}

		// 在 内层的 logger 不主动限制 level, 因为外层 helper 一定会限制 level 。
		// 如果没有显示指定 level，则用 debug 保证所有log都能打进去
		// 具体level限制由外层 helper 控制
		if fileLoggerCfg != nil && fileLoggerCfg.Level != nil {
			interCfg.Level = log.ParseLevel(fileLoggerCfg.GetLevel())
		} else {
			interCfg.Level = log.LevelDebug
		}

		// 文件轮换配置
		rotatingCfg := fileLoggerCfg.GetRotating()
		if !rotatingCfg.GetDisable() {
			interCfg.Rotating = &logger.RotatingFileLoggerConfig{
				MaxSize:    int(rotatingCfg.GetMaxSize()),
				MaxFileAge: int(rotatingCfg.GetMaxFileAge()),
				MaxFiles:   int(rotatingCfg.GetMaxFiles()),
				LocalTime:  rotatingCfg.GetLocalTime(),
				Compress:   rotatingCfg.GetCompress(),
			}
		}
		fileLogger, rc, err := logger.NewFileLogger(interCfg)
		if err != nil {
			return nil, nil, err
		}
		filterKeys := append(cfg.GetFilterKeys(), fileLoggerCfg.GetFilterKeys()...)
		fileFilterEmpty := filterEmpty
		if fileLoggerCfg != nil && fileLoggerCfg.FilterEmpty != nil {
			fileFilterEmpty = *fileLoggerCfg.FilterEmpty
		}
		if len(filterKeys) > 0 || fileFilterEmpty {
			fileLogger = logger.NewFilterKeysLogger(fileLogger, fileFilterEmpty, filterKeys...)
		}
		loggers = append(loggers, fileLogger)
		rcs = append(rcs, rc)
	}

	inner := logger.NewStackLogger(loggers...)

	l := &Log{
		nil,
		inner,
		[]any{
			TsKey, log.Timestamp(cfg.GetTimeFormat()),
			ServiceIDKey, serviceID,
			ServiceNameKey, serviceName,
			ServiceVersionKey, serviceVersion,
			TraceIDKey, tracing.TraceID(),
			SpanIDKey, tracing.SpanID(),
		},
		log.ParseLevel(cfg.GetLevel()),
		cfg.GetFilterKeys(),
		nil,
		nil,
		nil,
		defaultCallerDepth, // 默认caller深度6
		filterEmpty,
		"",
	}
	l.Helper = l.NewHelper()

	return l, func() {
		for _, rc := range rcs {
			rc()
		}
	}, nil
}

func (l *Log) WithCallerDepth(depth int32) *Log {
	ll := *l
	ll.callerDepth = depth
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithFilterEmpty(filterEmpty bool) *Log {
	ll := *l
	ll.filterEmpty = filterEmpty
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithLevel(lvl log.Level) *Log {
	ll := *l
	ll.level = lvl
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithFilterKeys(keys ...string) *Log {
	ll := *l
	ll.filterKeys = append(ll.filterKeys, keys...)
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithFilter(opts ...log.FilterOption) *Log {
	ll := *l
	ll.filterOpts = append(ll.filterOpts, opts...)
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) With(kv ...any) *Log {
	ll := *l
	ll.withKv = append(ll.withKv, kv...)
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithContext(ctx context.Context) *Log {
	ll := *l
	ll.withCtx = ctx
	ll.Helper = ll.NewHelper()
	return &ll
}

func (l *Log) WithModule(module string, optionalModuleLog ...ModuleConfig) *Log {
	if len(optionalModuleLog) == 0 {
		ll := *l
		ll.module = module
		ll.Helper = ll.NewHelper()
		return &ll
	}

	ll := l.WithModuleConfig(optionalModuleLog[0])
	ll.module = module
	ll.Helper = ll.NewHelper()
	return ll
}

func (l *Log) WithModuleConfig(moduleConfig ModuleConfig) *Log {
	ll := *l
	ll.Helper = ll.NewHelper()
	return ll.WithLevel(log.ParseLevel(utils.Select(moduleConfig.GetLevel(), l.level.String()))).
		WithFilterKeys(moduleConfig.GetFilterKeys()...)
}

func (l *Log) GetLogger() log.Logger {
	inner := l.inner

	// filterKeys 在kv内层，才能拦截过滤kv
	inner = logger.NewFilterKeysLogger(inner, l.filterEmpty, l.filterKeys...) // 这不是 log包 内部的日志结构，放在最底层

	// 过滤日志等级
	// filter opts 放在前面，才能过滤后面的kv
	filterOpts := append(l.filterOpts, log.FilterLevel(l.level))
	inner = log.NewFilter(inner, filterOpts...)

	// with kv
	var kv = l.withKv
	if l.module != "" {
		kv = append([]any{
			"module", l.module,
		}, kv...)
	}
	kv = append(l.getPresetKv(), kv...)
	inner = log.With(inner, kv...)

	// with ctx
	if l.withCtx != nil {
		inner = log.WithContext(l.withCtx, inner)
	}

	return inner
}

func (l *Log) NewHelper(opts ...log.Option) *log.Helper {
	helper := log.NewHelper(l.GetLogger(), opts...)
	return helper
}

func (l *Log) getPresetKv() []any {
	var kv []any
	filterKeys := l.filterKeys

	for i := 0; i < len(l.presetKv); i += 2 {
		var k = l.presetKv[i]
		var v = l.presetKv[i+1]
		if !utils.Includes(filterKeys, k.(string)) {
			kv = append(kv, k, v)
		}
	}

	if !utils.Includes(filterKeys, CallerKey) {
		kv = append(kv, CallerKey, log.Caller(int(l.callerDepth)))
	}

	return kv
}
