package log

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/internal/logger"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"google.golang.org/protobuf/proto"
)

// UpdateLogger 定义可动态更新配置的 Logger 接口
// 支持在运行时热更新日志配置，无需重启应用
type UpdateLogger interface {
	Update(config Config) error
}

// Logger 扩展 Kratos log.Logger，支持动态配置和链式调用
// 提供了灵活的日志配置能力，包括级别过滤、键过滤、上下文关联等
type Logger interface {
	log.Logger
	UpdateLogger
	// Disable 禁用或启用日志输出，可选参数指定是否禁用（默认 true）
	// 返回新的 Logger 实例，不影响原实例
	Disable(...bool) Logger
	// FilterLevel 设置日志过滤级别，低于该级别的日志不会被输出
	// 返回新的 Logger 实例，不影响原实例
	FilterLevel(level log.Level) Logger
	// With 添加键值对到日志上下文，返回新的 Logger 实例
	// 这些键值对会包含在该实例输出的所有日志中
	With(kv ...any) Logger
	// FilterKeys 设置需要过滤的键列表，这些键不会被记录到日志中
	// 返回新的 Logger 实例，不影响原实例
	FilterKeys(keys ...string) Logger
	// WithContext 将 context 关联到 logger，用于追踪链路信息
	// 返回新的 Logger 实例，不影响原实例
	WithContext(context.Context) Logger
	// WithCallerDepth 设置调用栈深度，用于定位日志调用位置
	// 返回新的 Logger 实例，不影响原实例
	WithCallerDepth(int) Logger
	// AddCallerDepth 在当前调用栈深度基础上增加偏移量
	// 返回新的 Logger 实例，不影响原实例
	AddCallerDepth(...int) Logger
}

// globalLogger 保存全局日志配置，通过 atomic.Value 保证并发安全
// 所有 Logger 实例共享同一份全局配置
type globalLogger struct {
	timestamp  time.Time  // 配置更新时间戳，用于缓存失效判断
	coreLogger log.Logger // 核心日志输出器（未包含预设 KV）
	kvLogger   log.Logger // 包含预设 KV 的日志器
	logger     log.Logger // 最终日志器（包含 KV 和级别过滤）
	level      log.Level  // 全局日志级别
	kv         []any      // 预设的键值对
	hasValuer  bool       // 是否包含 Valuer 类型（需要每次求值）
	hasCaller  bool       // 是否包含调用者信息
}

// cacheLogger 缓存多层日志器，避免每次 Log 都重新构建
// 采用分层缓存策略：
//   layer1(key过滤) -> layer2(预设KV) -> layer3(动态KV) -> layer4(context)
// 这种分层设计允许高效地复用中间层
type cacheLogger struct {
	log.Logger
	timestamp time.Time // 对应 global 的 timestamp，用于判断缓存是否有效

	layer1 log.Logger // 第一层：键过滤层
	layer2 log.Logger // 第二层：预设 KV 层
	layer3 log.Logger // 第三层：动态 KV 层

	filterKeys  map[string]struct{} // 需要过滤的键集合
	callerDepth int                 // 调用栈深度
	kv          []any               // 动态键值对
	ctx         context.Context     // 关联的上下文
}

// logger Logger 接口的实现，支持动态配置和链式调用
// 采用写时复制（copy-on-write）模式，每次 With/FilterKeys 等操作返回新实例
type logger struct {
	global        *atomic.Value // 存储 *globalLogger，保证并发安全
	release       *atomic.Value // 存储资源释放函数 func()
	presetKv      PresetKv      // 预设键值对生成器
	defaultConfig DefaultConfig // 默认配置
	hook          Hook          // 日志钩子

	cache atomic.Value // 存储 *cacheLogger，缓存分层日志器

	disable     bool                // 是否禁用日志
	level       *log.Level          // 实例级别的日志过滤级别（nil 表示使用全局级别）
	filterKeys  map[string]struct{} // 实例级别的键过滤
	callerDepth int                 // 实例级别的调用栈深度
	kv          []any               // 实例级别的键值对
	ctx         context.Context     // 关联的上下文
}

// NewLogger 创建一个新的 Logger 实例
// 参数：
//   - presetKv: 预设键值对生成器，用于生成日志公共字段
//   - defaultConfig: 默认日志配置
//   - hook: 日志生命周期钩子
//
// 返回：
//   - Logger: 日志实例
//   - func(): 资源释放函数，用于关闭日志文件等清理工作
//   - error: 错误信息
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
		cache:         atomic.Value{},
		disable:       false,
		level:         nil,
		filterKeys:    nil,
		callerDepth:   0,
		kv:            nil,
		ctx:           nil,
	}
	// 使用默认 logger
	l.global.Store(&globalLogger{
		timestamp:  time.Now(),
		coreLogger: log.GetLogger(),
		kvLogger:   log.GetLogger(),
		logger:     log.GetLogger(),
		level:      log.LevelDebug,
		kv:         nil,
		hasValuer:  false,
		hasCaller:  false,
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

// Log 实现 log.Logger 接口，输出日志
// 执行流程：
// 1. 检查是否禁用
// 2. 检查日志级别
// 3. 检查缓存有效性，必要时重建缓存
// 4. 委托给缓存的日志器执行
func (l *logger) Log(level log.Level, keyvals ...any) error {
	// 如果禁用，则不输出日志
	if l.disable {
		return nil
	}
	// 优先过滤 l.level（实例级别）
	if l.level != nil {
		if level < *l.level {
			return nil
		}
	}
	global := l.global.Load().(*globalLogger)
	// 如果实例级别未设置，则使用全局级别
	if l.level == nil && level < global.level {
		return nil
	}

	// 检查缓存是否过期，global.timestamp 变更表示配置已更新
	cache := l.cache.Load().(*cacheLogger)
	if !cache.timestamp.Equal(global.timestamp) {
		l.buildCache()
		cache = l.cache.Load().(*cacheLogger)
	}

	return cache.Log(level, keyvals...)
}

// Disable 禁用或启用日志输出
// 参数：optionalDisable - 可选，指定是否禁用（默认 true）
func (l *logger) Disable(optionalDisable ...bool) Logger {
	ll := *l
	var disable = true
	if len(optionalDisable) > 0 {
		disable = optionalDisable[0]
	}
	ll.disable = disable
	return &ll
}

// FilterLevel 设置日志过滤级别，低于该级别的日志不会被输出
func (l *logger) FilterLevel(level log.Level) Logger {
	ll := *l
	ll.level = &level
	return &ll
}

// With 添加键值对到日志上下文，返回新的 Logger 实例
// 注意：该方法会复制当前 logger 并重建缓存
func (l *logger) With(kv ...any) Logger {
	ll := *l
	ll.kv = append(ll.kv, kv...)
	ll.buildCache()
	return &ll
}

// FilterKeys 设置需要过滤的键列表，这些键不会被记录到日志中
// 会合并已有的过滤键和新的过滤键
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

// WithContext 将 context 关联到 logger，用于追踪链路信息
func (l *logger) WithContext(ctx context.Context) Logger {
	ll := *l
	ll.ctx = ctx
	ll.buildCache()
	return &ll
}

// WithCallerDepth 设置调用栈深度，用于定位日志调用位置
func (l *logger) WithCallerDepth(callerDepth int) Logger {
	ll := *l
	ll.callerDepth = callerDepth
	ll.buildCache()
	return &ll
}

// AddCallerDepth 在当前调用栈深度基础上增加偏移量
// 参数：optionalCallerDepth - 可选，指定增加的深度（默认 1）
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

// buildLayer1 构建第一层日志器：键过滤层
// 如果实例没有设置过滤键，直接返回全局 coreLogger
func (l *logger) buildLayer1(global *globalLogger) log.Logger {
	if len(l.filterKeys) == 0 {
		return global.coreLogger
	}
	return internal_logger.NewFilterLogger(global.coreLogger, false, l.filterKeys)
}

// cacheLayer1 获取或构建第一层日志器（带缓存）
func (l *logger) cacheLayer1(global *globalLogger, cache *cacheLogger) log.Logger {
	layer1 := cache.layer1
	if layer1 == nil {
		layer1 = l.buildLayer1(global)
	}
	return layer1
}

// buildLayer2 构建第二层日志器：预设 KV 层
// 如果实例没有设置 callerDepth，直接使用全局 kvLogger（已包含预设 KV）
// 否则需要重新构建，将 callerKey 的值替换为指定深度的调用者信息
func (l *logger) buildLayer2(global *globalLogger, layer1 log.Logger) log.Logger {
	if l.callerDepth == 0 {
		// 使用默认 caller 深度，直接复用全局 kvLogger
		if layer1 == global.coreLogger {
			return global.kvLogger
		}
		return log.With(layer1, global.kv...)
	}

	// 需要自定义 callerDepth，重新构建 KV
	length := len(global.kv)
	kv := make([]any, 0, length)
	for i := 0; i < length; i += 2 {
		key, ok := global.kv[i].(string)
		if !ok {
			continue
		}
		kv = append(kv, key)
		// 检查是否有对应的值
		if i+1 > length-1 {
			break
		}
		val := global.kv[i+1]
		// 替换 callerKey 的值为指定深度的调用者
		if key == callerKey {
			val = log.Caller(l.callerDepth)
		}
		kv = append(kv, val)
	}
	return log.With(layer1, kv...)
}

// cacheLayer2 获取或构建第二层日志器（带缓存）
func (l *logger) cacheLayer2(global *globalLogger, cache *cacheLogger) log.Logger {
	layer2 := cache.layer2
	if layer2 == nil {
		layer2 = l.buildLayer2(global, l.cacheLayer1(global, cache))
	}
	return layer2
}

// buildLayer3 构建第三层日志器：动态 KV 层
// 将实例级别的键值对（通过 With 方法添加）添加到日志器中
func (l *logger) buildLayer3(layer2 log.Logger) log.Logger {
	if len(l.kv) == 0 {
		return layer2
	}
	return log.With(layer2, l.kv...)
}

// cacheLayer3 获取或构建第三层日志器（带缓存）
func (l *logger) cacheLayer3(global *globalLogger, cache *cacheLogger) log.Logger {
	layer3 := cache.layer3
	if layer3 == nil {
		layer3 = l.buildLayer3(l.cacheLayer2(global, cache))
	}
	return layer3
}

// buildLayer4 构建第四层日志器：context 层
// 将 context 关联到日志器，用于链路追踪（提取 trace_id、span_id 等）
func (l *logger) buildLayer4(layer3 log.Logger) log.Logger {
	if l.ctx == nil {
		return layer3
	}
	return log.WithContext(l.ctx, layer3)
}

// buildCache 构建或更新日志器缓存
// 采用增量更新策略：只有变化的层才重新构建，其他层复用缓存
// 缓存失效条件：
//  1. global.timestamp 变更（全局配置更新）
//  2. 实例的 filterKeys 变更
//
// 缓存优化策略：
//   - 如果 filterKeys 未变，只重建变化的层
//   - callerDepth 变化：重建 layer2, layer3, layer4
//   - kv 变化：重建 layer3, layer4
//   - ctx 变化：重建 layer4
func (l *logger) buildCache() {
	if l.disable {
		return
	}

	// 获取全局配置
	global := l.global.Load().(*globalLogger)
	// 如果 global 没有 caller，则实例的 callerDepth 无效
	if !global.hasCaller {
		l.callerDepth = 0
	}

	// 快速路径：如果实例没有自定义参数，直接使用全局 logger
	if len(l.kv) == 0 && len(l.filterKeys) == 0 && l.callerDepth == 0 && l.ctx == nil {
		l.cache.Store(&cacheLogger{
			Logger:      global.logger,
			timestamp:   global.timestamp,
			layer1:      global.coreLogger,
			layer2:      global.kvLogger,
			layer3:      global.kvLogger,
			filterKeys:  l.filterKeys,
			callerDepth: l.callerDepth,
			kv:          l.kv,
			ctx:         l.ctx,
		})
		return
	}

	cache := l.cache.Load().(*cacheLogger)

	// 增量更新路径：global 配置未变且 filterKeys 未变
	if global.timestamp == cache.timestamp && reflect.DeepEqual(l.filterKeys, cache.filterKeys) {
		var layer1, layer2, layer3, layer4 log.Logger

		// 根据变化的层，选择不同的重建策略
		if l.callerDepth != cache.callerDepth {
			// callerDepth 变化：重建 layer2 及以上
			layer1 = l.cacheLayer1(global, cache)
			layer2 = l.buildLayer2(global, layer1)
			layer3 = l.buildLayer3(layer2)
			layer4 = l.buildLayer4(layer3)
		} else if !reflect.DeepEqual(l.kv, cache.kv) {
			// kv 变化：重建 layer3 及以上
			layer1 = l.cacheLayer1(global, cache)
			layer2 = l.cacheLayer2(global, cache)
			layer3 = l.buildLayer3(layer2)
			layer4 = l.buildLayer4(layer3)
		} else if l.ctx != cache.ctx {
			// ctx 变化：重建 layer4
			layer1 = l.cacheLayer1(global, cache)
			layer2 = l.cacheLayer2(global, cache)
			layer3 = l.cacheLayer3(global, cache)
			layer4 = cache.Logger
			if layer4 == nil {
				layer4 = l.buildLayer4(layer3)
			}
		} else {
			// 无变化，复用所有层
			layer1 = l.cacheLayer1(global, cache)
			layer2 = l.cacheLayer2(global, cache)
			layer3 = l.cacheLayer3(global, cache)
			layer4 = cache.Logger
			if layer4 == nil {
				layer4 = l.buildLayer4(layer3)
			}
		}
		l.cache.Store(&cacheLogger{
			Logger:      layer4,
			timestamp:   global.timestamp,
			layer1:      layer1,
			layer2:      layer2,
			layer3:      layer3,
			filterKeys:  l.filterKeys,
			callerDepth: l.callerDepth,
			kv:          l.kv,
			ctx:         l.ctx,
		})
		return
	}

	// 完全重建路径：global 配置变更或 filterKeys 变更
	layer1 := l.buildLayer1(global)
	layer2 := l.buildLayer2(global, layer1)
	layer3 := l.buildLayer3(layer2)
	layer4 := l.buildLayer4(layer3)
	l.cache.Store(&cacheLogger{
		Logger:      layer4,
		timestamp:   global.timestamp,
		layer1:      layer1,
		layer2:      layer2,
		layer3:      layer3,
		filterKeys:  l.filterKeys,
		callerDepth: l.callerDepth,
		kv:          l.kv,
		ctx:         l.ctx,
	})
}

// Update 动态更新日志配置
// 支持热更新日志输出目标（文件/标准输出）、日志级别、过滤规则等
//
// 更新流程：
// 1. 合并默认配置和传入配置
// 2. 根据配置创建文件日志器和标准输出日志器
// 3. 为每个日志器应用键过滤和级别过滤
// 4. 合并所有日志器为统一的 coreLogger
// 5. 添加预设 KV（时间戳、调用者等）
// 6. 释放旧的日志器资源，保存新的全局配置
func (l *logger) Update(c Config) error {
	// 合并默认配置和传入配置
	config := proto.CloneOf((Config)(l.defaultConfig))
	proto.Merge(config, c)

	var loggers []log.Logger
	var rcs []func()

	// 创建文件日志器
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

		// 应用键过滤和级别过滤
		fileLogger = internal_logger.NewFilterLogger(fileLogger, false, filterKeys(file.GetFilterKeys()))

		level := log.LevelDebug
		if file != nil && file.Level != nil {
			level = log.ParseLevel(file.GetLevel())
		}
		fileLogger = internal_logger.NewFilterLevelLogger(fileLogger, level)
		loggers = append(loggers, fileLogger)
	}

	// 创建标准输出日志器
	if std := config.GetStd(); !std.GetDisable() {
		stdLogger := internal_logger.NewStdLogger()
		stdLogger = internal_logger.NewFilterLogger(stdLogger, false, filterKeys(std.GetFilterKeys()))
		level := log.LevelDebug
		if std != nil && std.Level != nil {
			level = log.ParseLevel(std.GetLevel())
		}
		stdLogger = internal_logger.NewFilterLevelLogger(stdLogger, level)
		loggers = append(loggers, stdLogger)
	}

	// 合并所有日志器为 coreLogger
	coreLogger := internal_logger.NewStackLogger(loggers...)
	coreLogger = internal_logger.NewFilterLogger(coreLogger, config.GetFilterEmpty(), filterKeys(config.GetFilterKeys()))
	logger := coreLogger

	// 添加预设 KV
	var kv []any
	preset := config.GetPreset()
	if len(preset) == 0 {
		preset = defaultPreset
	}
	preset = utils.Unique(preset)
	var hasCaller bool
	for _, key := range preset {
		val, ok := l.presetKv[key]
		if !ok {
			continue
		}
		// 时间戳字段支持自定义格式
		if key == tsKey {
			if config.GetTimeFormat() != "" {
				kv = append(kv, key, log.Timestamp(config.GetTimeFormat()))
			} else {
				kv = append(kv, key, val)
			}
		} else {
			if key == callerKey {
				hasCaller = true
			}
			kv = append(kv, key, val)
		}
	}

	// 添加 hook 的全局 KV
	if h, ok := l.hook.(*hook); ok {
		kv = append(kv, h.kv...)
	} else {
		return errors.New("log.Hook does not implement hook")
	}
	logger = log.With(logger, kv...)
	kvLogger := logger

	// 应用全局日志级别过滤
	level := log.LevelDebug
	if config.GetLevel() != "" {
		level = log.ParseLevel(config.GetLevel())
	}
	logger = log.NewFilter(logger, log.FilterLevel(level))

	// 释放旧的日志器资源（关闭文件句柄等）
	old := l.release.Swap(func() {
		for _, rc := range rcs {
			rc()
		}
	})
	if old != nil {
		old.(func())()
	}

	// 保存新的全局配置
	l.global.Store(&globalLogger{
		timestamp:  time.Now(),
		coreLogger: coreLogger,
		kvLogger:   kvLogger,
		logger:     logger,
		level:      level,
		kv:         kv,
		hasValuer:  containsValuer(kv),
		hasCaller:  hasCaller,
	})
	return nil
}

// filterKeys 将字符串切片转换为 map[string]struct{}，用于高效的键查找和过滤
// 使用空结构体作为值可以减少内存占用
func filterKeys(filterKeys []string) map[string]struct{} {
	ret := make(map[string]struct{})
	for _, key := range filterKeys {
		ret[key] = struct{}{}
	}
	return ret
}
