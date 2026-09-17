package log

import (
	"fmt"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

// eventFields 接收纯字段处理链的结果，随后才进入实际输出的生命周期边界。
type eventFields []any

func (e *eventFields) Log(_ kratoslog.Level, fields ...any) error {
	*e = fields
	return nil
}

// formattedKey 保留非字符串 key 不参与过滤和去重的原有语义。
type formattedKey string

// formattedField 延迟并记忆单字段格式化，策略重试时保留尚未放行的原始值。
// 仅在当前日志调用内使用，不会交给输出端或并发共享。
type formattedField struct {
	// value 尚未格式化的原始字段值。
	value any
	// format 字段使用的格式化模板。
	format string
	// text 首次格式化后的缓存文本。
	text string
	// done 标记文本已生成，避免重复调用字段格式化方法。
	done bool
}

func (f *formattedField) String() string {
	if !f.done {
		f.text = fmt.Sprintf(f.format, f.value)
		f.done = true
	}
	return f.text
}

func memoizeFields(fields []any) []any {
	for i, value := range fields {
		if _, plain := value.(string); plain || value == nil {
			continue
		}
		format := "%v"
		if i%2 == 0 {
			format = "%s"
		}
		fields[i] = &formattedField{value: value, format: format}
	}
	return fields
}

func freezeFields(fields []any) []any {
	result := make([]any, len(fields))
	for i, value := range fields {
		if i%2 == 0 {
			if key, ok := value.(string); ok {
				result[i] = key
			} else {
				result[i] = formattedKey(fmt.Sprintf("%s", value))
			}
		} else if value != nil {
			result[i] = fmt.Sprintf("%v", value)
		}
	}
	return result
}

func (l *logger) currentCache() *loggerCache {
	for {
		custom := l.shared.custom.Load()
		if l.expired(custom) && !l.buildCache(custom) {
			continue
		}
		l.mu.RLock()
		cache := l.cache
		l.mu.RUnlock()
		if cache.customVersion == custom.version {
			return cache
		}
	}
}

// loggerCache 保存指定共享版本的不可变字段与过滤规则。
type loggerCache struct {
	// customVersion 构造缓存时的共享策略版本，用于检测失效。
	customVersion uint64
	// msgKey 消息正文的字段名。
	msgKey string
	// fields 已合并的预置及上下文字段；保留原始值和 Valuer，不是已格式化文本。
	fields []any
	// filterKeys 当前版本的字段过滤集合。
	filterKeys map[string]struct{}
}

func (l *logger) expired(custom *customState) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.cache == nil || l.cache.customVersion != custom.version
}

// buildCache 在锁外构造字段快照，仅在版本仍有效时持锁发布。
func (l *logger) buildCache(custom *customState) bool {
	if l.shared.custom.Load() != custom {
		return false
	}
	l.mu.RLock()
	cached := l.cache != nil && l.cache.customVersion == custom.version
	l.mu.RUnlock()
	if cached {
		return true
	}

	config := l.config
	filterKeys := make([]string, 0, len(config.filterKeys)+len(custom.filterKeys)+len(l.filterKeys))
	if custom.filterKeys != nil {
		filterKeys = append(filterKeys, custom.filterKeys...)
	} else {
		filterKeys = append(filterKeys, config.filterKeys...)
	}
	if override := custom.policy.matchModule(l.module); override != nil {
		filterKeys = append(filterKeys, override.FilterKeys...)
	}
	filterKeys = append(filterKeys, l.filterKeys...)

	timeFormat := config.timeFormat
	depth := l.callerDepth
	if depth <= 0 {
		depth = defaultCallerDepth
	}
	preset := newPreset(timeFormat, caller(depth))
	kvs := make([]any, 0, len(preset)+len(custom.kv)+len(l.kv)+2)
	kvs = append(kvs, preset...)
	if l.module != "" {
		kvs = append(kvs, moduleKey, l.module)
	}
	kvs = appendNonModuleFields(kvs, custom.kv)
	kvs = append(kvs, l.kv...)
	if l.ctx != nil {
		kvs = appendNonModuleFields(kvs, kvFromCtx(l.ctx))
	}

	msgKey := config.msgKey

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shared.custom.Load() != custom {
		return false
	}
	l.cache = &loggerCache{
		customVersion: custom.version,
		fields:        kvs,
		filterKeys:    output.FilterKeysSet(filterKeys),
		msgKey:        msgKey,
	}
	return true
}

// appendNonModuleFields 防止进程共享字段和请求上下文改变事件所属模块。
func appendNonModuleFields(dst, fields []any) []any {
	for i := 0; i < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok && key == moduleKey {
			continue
		}
		dst = append(dst, fields[i])
		if i+1 < len(fields) {
			dst = append(dst, fields[i+1])
		} else {
			dst = append(dst, "(MISSING)")
		}
	}
	return dst
}
