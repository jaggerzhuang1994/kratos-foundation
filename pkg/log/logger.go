package log

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/logger"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"google.golang.org/protobuf/proto"
)

type UpdateLogger interface {
	Update(config Config) error
}

type Logger interface {
	log.Logger
	UpdateLogger
	Disable(...bool) Logger
	With(kv ...any) Logger
	FilterLevel(level log.Level) Logger
	FilterKeys(keys ...string) Logger
	WithContext(context.Context) Logger
	WithCallerDepth(int) Logger
	AddCallerDepth(...int) Logger
}

type globalLogger struct {
	timestamp time.Time
	core      log.Logger
	logger    log.Logger
	level     log.Level
	kv        []any
	hasValuer bool
}

type cacheLogger struct {
	log.Logger
	timestamp time.Time
}

type logger struct {
	global        *atomic.Value
	release       *atomic.Value
	presetKv      PresetKv
	defaultConfig DefaultConfig
	hook          Hook

	cache atomic.Value

	disable     bool
	level       *log.Level
	kv          []any
	filterKeys  map[string]struct{}
	ctx         context.Context
	callerDepth int
}

func NewLogger(
	presetKv PresetKv,
	defaultConfig DefaultConfig,
	hook Hook,
) (Logger, func(), error) {
	l := &logger{
		global:        new(atomic.Value),
		release:       new(atomic.Value),
		presetKv:      presetKv,
		defaultConfig: defaultConfig,
		hook:          hook,
	}
	// 使用默认 logger
	l.global.Store(&globalLogger{
		timestamp: time.Now(),
		core:      log.GetLogger(),
		logger:    log.GetLogger(),
		level:     log.LevelDebug,
	})
	err := l.Update(nil)
	if err != nil {
		return nil, nil, err
	}
	l.buildCache()
	return l, func() {
		l.release.Load().(func())()
	}, nil
}

func (l *logger) buildCache() {
	if l.disable {
		return
	}
	global := l.global.Load().(*globalLogger)

	// 如果没有自定义输出规则，则使用 logger 输出
	if l.level == nil && len(l.kv) == 0 && len(l.filterKeys) == 0 && l.callerDepth == 0 && (l.ctx == nil || !global.hasValuer) {
		l.cache.Store(&cacheLogger{
			Logger:    global.logger,
			timestamp: global.timestamp,
		})
		return
	}

	logger := global.core

	keyvals := append(global.kv, l.kv...)
	length := len(keyvals)
	kv := make([]any, 0, length)
	for i := 0; i < length; i += 2 {
		key, ok := keyvals[i].(string)
		if ok {
			if _, ok := l.filterKeys[key]; ok {
				continue
			}
		}
		kv = append(kv, keyvals[i])
		// no value pair
		if i+1 > length-1 {
			continue
		}
		val := keyvals[i+1]
		if l.callerDepth > 0 && key == callerKey {
			val = log.Caller(l.callerDepth)
		}
		kv = append(kv, val)
	}
	logger = log.With(logger, kv...)

	if l.ctx != nil {
		logger = log.WithContext(l.ctx, logger)
	}

	var level = global.level
	if l.level != nil {
		level = *l.level
	}
	logger = log.NewFilter(logger, log.FilterLevel(level))

	l.cache.Store(&cacheLogger{
		Logger:    logger,
		timestamp: global.timestamp,
	})
}

func (l *logger) Log(level log.Level, keyvals ...any) error {
	// 如果禁用，则不输出日志
	if l.disable {
		return nil
	}
	global := l.global.Load().(*globalLogger)
	cache := l.cache.Load().(*cacheLogger)
	if !cache.timestamp.Equal(global.timestamp) {
		l.buildCache()
		cache = l.cache.Load().(*cacheLogger)
	}

	return cache.Log(level, keyvals...)
}

func (l *logger) Disable(optionalDisable ...bool) Logger {
	ll := *l
	var disable = true
	if len(optionalDisable) > 0 {
		disable = optionalDisable[0]
	}
	ll.disable = disable
	ll.buildCache()
	return &ll
}

func (l *logger) With(kv ...any) Logger {
	ll := *l
	ll.kv = append(ll.kv, kv...)
	ll.buildCache()
	return &ll
}

func (l *logger) FilterLevel(level log.Level) Logger {
	ll := *l
	ll.level = &level
	ll.buildCache()
	return &ll
}

func (l *logger) FilterKeys(keys ...string) Logger {
	ll := *l
	filterKeys := make(map[string]struct{}, len(keys)+len(ll.filterKeys))
	for key := range ll.filterKeys {
		filterKeys[key] = struct{}{}
	}
	for _, key := range keys {
		filterKeys[key] = struct{}{}
	}
	ll.filterKeys = filterKeys
	ll.buildCache()
	return &ll
}

func (l *logger) WithContext(ctx context.Context) Logger {
	ll := *l
	ll.ctx = ctx
	ll.buildCache()
	return &ll
}

func (l *logger) WithCallerDepth(callerDepth int) Logger {
	ll := *l
	ll.callerDepth = callerDepth
	ll.buildCache()
	return &ll
}

func (l *logger) AddCallerDepth(optionalCallerDepth ...int) Logger {
	ll := *l
	var callerDepth = 1
	if len(optionalCallerDepth) > 0 {
		callerDepth = optionalCallerDepth[0]
	}
	if ll.callerDepth == 0 {
		ll.callerDepth = defaultCallerDepth + callerDepth
	} else {
		ll.callerDepth += callerDepth
	}
	ll.buildCache()
	return &ll
}

func (l *logger) Update(c Config) error {
	config := proto.CloneOf((Config)(l.defaultConfig))
	proto.Merge(config, c)

	var loggers []log.Logger
	var rcs []func()

	// 文件日志
	if file := config.GetFile(); !file.GetDisable() {
		// 内部文件日志配置
		fileLoggerConf := internal_logger.FileLoggerConfig{
			Path:     file.GetPath(),
			Rotating: nil,
		}
		// 文件轮换配置
		if rotating := file.GetRotating(); !rotating.GetDisable() {
			fileLoggerConf.Rotating = &internal_logger.RotatingFileLoggerConfig{
				MaxSize:    int(rotating.GetMaxSize()),
				MaxFileAge: int(rotating.GetMaxFileAge()),
				MaxFiles:   int(rotating.GetMaxFiles()),
				LocalTime:  rotating.GetLocalTime(),
				Compress:   rotating.GetCompress(),
			}
		}

		fileLogger, rc, err := internal_logger.NewFileLogger(fileLoggerConf)
		if err != nil {
			return err
		}
		rcs = append(rcs, rc)

		fileLogger = internal_logger.NewFilterLogger(fileLogger, false, file.GetFilterKeys())

		level := log.LevelDebug
		if file != nil && file.Level != nil {
			level = log.ParseLevel(file.GetLevel())
		}
		fileLogger = internal_logger.NewFilterLevelLogger(fileLogger, level)
		loggers = append(loggers, fileLogger)
	}

	// 标准输出流日志
	if std := config.GetStd(); !std.GetDisable() {
		stdLogger := internal_logger.NewStdLogger()
		stdLogger = internal_logger.NewFilterLogger(stdLogger, false, std.GetFilterKeys())
		level := log.LevelDebug
		if std != nil && std.Level != nil {
			level = log.ParseLevel(std.GetLevel())
		}
		stdLogger = internal_logger.NewFilterLevelLogger(stdLogger, level)
		loggers = append(loggers, stdLogger)
	}

	// core logger
	core := internal_logger.NewStackLogger(loggers...)
	core = internal_logger.NewFilterLogger(core, config.GetFilterEmpty(), config.GetFilterKeys())
	logger := core

	// preset kv
	var kv []any
	preset := config.GetPreset()
	if len(preset) == 0 {
		preset = defaultPreset
	}
	preset = utils.Unique(preset)
	for _, key := range preset {
		val, ok := l.presetKv[key]
		if !ok {
			continue
		}
		if key == tsKey {
			if config.GetTimeFormat() != "" {
				kv = append(kv, key, log.Timestamp(config.GetTimeFormat()))
			} else {
				kv = append(kv, key, val)
			}
		} else {
			kv = append(kv, key, val)
		}
	}
	if h, ok := l.hook.(hookInternal); ok {
		kv = append(kv, h.customKv()...)
	}
	logger = log.With(logger, kv...)

	level := log.LevelDebug
	if config.GetLevel() != "" {
		level = log.ParseLevel(config.GetLevel())
	}
	logger = log.NewFilter(logger, log.FilterLevel(level))

	old := l.release.Swap(func() {
		for _, rc := range rcs {
			rc()
		}
	})
	if old != nil { // 释放之前的 logger
		old.(func())()
	}
	l.global.Store(&globalLogger{
		timestamp: time.Now(),
		core:      core,
		logger:    logger,
		level:     level,
		kv:        kv,
		hasValuer: containsValuer(kv),
	})
	return nil
}
