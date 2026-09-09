package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

// loggerCache 保存一组精确 Config/Custom 版本对应的写入链。
type loggerCache struct {
	configVersion uint64
	customVersion uint64
	msgKey        string
	logger        kratoslog.Logger
}

func (l *logger) expired(config *configState, custom *customState) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.cache == nil || l.cache.configVersion != config.version ||
		l.cache.customVersion != custom.version
}

// buildCache 仅发布仍与 SharedState 当前快照一致的写入链。
func (l *logger) buildCache(config *configState, custom *customState) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shared.config.Load() != config || l.shared.custom.Load() != custom {
		return false
	}
	if l.cache != nil && l.cache.configVersion == config.version &&
		l.cache.customVersion == custom.version {
		return true
	}

	cache := kratoslog.Logger(config.output)
	cache = output.NewDedupe(cache)

	filterEmpty := config.filterEmpty
	if l.filterEmpty != nil {
		filterEmpty = *l.filterEmpty
	} else if custom.filterEmpty != nil {
		filterEmpty = *custom.filterEmpty
	}
	filterKeys := make([]string, 0, len(config.filterKeys)+len(custom.filterKeys)+len(l.filterKeys))
	filterKeys = append(filterKeys, config.filterKeys...)
	filterKeys = append(filterKeys, custom.filterKeys...)
	filterKeys = append(filterKeys, l.filterKeys...)
	cache = output.NewFilter(cache, filterEmpty, output.FilterKeysSet(filterKeys))

	timeFormat := config.timeFormat
	if l.timeFormat != "" {
		timeFormat = l.timeFormat
	} else if custom.timeFormat != "" {
		timeFormat = custom.timeFormat
	}
	callerDepth := config.callerDepth
	if l.callerDepth > 0 {
		callerDepth = l.callerDepth
	} else if custom.callerDepth > 0 {
		callerDepth = custom.callerDepth
	}
	callerDepth += l.callerDepthDelta
	preset := newPreset(timeFormat, callerDepth)
	kvs := make([]any, 0, len(preset)+len(custom.kv)+len(l.kv)+2)
	kvs = append(kvs, preset...)
	if l.module != "" {
		kvs = append(kvs, moduleKey, l.module)
	}
	kvs = append(kvs, custom.kv...)
	kvs = append(kvs, l.kv...)
	if l.ctx != nil {
		kvs = append(kvs, KvFromCtx(l.ctx)...)
	}
	cache = kratoslog.With(cache, kvs...)

	if l.ctx != nil {
		cache = kratoslog.WithContext(l.ctx, cache)
	}

	msgKey := config.msgKey
	if l.msgKey != "" {
		msgKey = l.msgKey
	} else if custom.msgKey != "" {
		msgKey = custom.msgKey
	}

	l.cache = &loggerCache{
		configVersion: config.version,
		customVersion: custom.version,
		logger:        cache,
		msgKey:        msgKey,
	}
	return true
}
