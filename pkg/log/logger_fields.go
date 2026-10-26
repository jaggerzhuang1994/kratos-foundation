package log

import (
	"fmt"
	"strings"
)

// formattedKey 保留非字符串 key 不参与过滤和去重的原有语义。
type formattedKey string

// loggerCache 保存指定共享策略版本下的不可变字段与预编译过滤规则。
// 它只由派生 Logger 的固定配置和该版本共享快照决定，版本变化即重建，不需要额外的失效时机。
type loggerCache struct {
	// customVersion 构造缓存时的共享策略版本，用于检测失效。
	customVersion uint64
	// msgKey 消息正文的字段名。
	msgKey string
	// fields 已合并的预置、共享与实例字段；不含 Context 字段，保留原始值和 Valuer，不是已格式化文本。
	fields []any
	// filter 当前版本预编译的字段过滤规则。
	filter keyFilter
	// module 是派生 Logger 的固定模块（已装箱）；nil 表示按事件字段推断。
	module any
}

// unknownModule 是无法确定模块时的归属值。
var unknownModule any = "unknown"

// currentCache 返回与 custom 版本一致的缓存；并发构造的结果等价，后写者覆盖不影响正确性。
func (l *logger) currentCache(custom *customState) *loggerCache {
	if cache := l.cache.Load(); cache != nil && cache.customVersion == custom.version {
		return cache
	}
	cache := l.buildCache(custom)
	l.cache.Store(cache)
	return cache
}

// buildCache 按共享快照合并预置字段、共享字段与实例字段，并编译过滤规则。
func (l *logger) buildCache(custom *customState) *loggerCache {
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

	depth := l.callerDepth
	if depth <= 0 {
		depth = defaultCallerDepth
	}
	preset := newPreset(config.timeFormat, caller(depth))
	kvs := make([]any, 0, len(preset)+len(custom.kv)+len(l.kv)+2)
	kvs = append(kvs, preset...)
	if l.module != "" {
		kvs = append(kvs, moduleKey, l.module)
	}
	kvs = appendNonModuleFields(kvs, custom.kv)
	kvs = append(kvs, l.kv...)
	// Context 字段在每次写入时追加，不进入缓存，使 WithContext 派生的请求级 Logger 能复用父缓存。
	cache := &loggerCache{
		customVersion: custom.version,
		fields:        kvs,
		filter:        newKeyFilter(filterKeys),
		msgKey:        config.msgKey,
	}
	if l.module != "" {
		cache.module = l.module
	}
	return cache
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

// keyFilter 是预编译的字段过滤规则：精确 key 与尾部 * 前缀；module 是归属契约，始终保留。
type keyFilter struct {
	exact    map[string]struct{}
	prefixes []string
}

func newKeyFilter(keys []string) keyFilter {
	var filter keyFilter
	for _, key := range keys {
		if prefix, ok := strings.CutSuffix(key, "*"); ok {
			filter.prefixes = append(filter.prefixes, prefix)
			continue
		}
		if filter.exact == nil {
			filter.exact = make(map[string]struct{}, len(keys))
		}
		filter.exact[key] = struct{}{}
	}
	return filter
}

func (f keyFilter) drops(key string) bool {
	if key == moduleKey {
		return false
	}
	if _, ok := f.exact[key]; ok {
		return true
	}
	for _, prefix := range f.prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// slotMissing 表示孤立 key 没有对应值，输出时补齐为 "(MISSING)"。
const slotMissing = -1

// fieldPair 以下标引用事件字段，避免在过滤和重排阶段复制或格式化值。
type fieldPair struct{ key, value int }

// render 把一次事件的字段依次处理为最终输出：
//
//  1. 按 key 过滤（module 保留；非字符串 key 不参与过滤）；
//  2. 归一化为恰好一个 module（缓存的固定模块优先，否则取最后一个有效声明，缺失为 unknown），
//     并按 ts、module、caller、其余字段排列，孤立 key 补齐 "(MISSING)"；
//  3. 内置输出时把 key、value 格式化为文本，外部输出保留原始类型；
//  4. 启用 filterEmpty 时移除 nil 或格式化为空的值；
//  5. 重复的字符串 key 保留首次位置、取最后一个值。
//
// texts 缓存内置输出按原始下标格式化的文本：策略重试时不会重复调用字段的格式化方法，
// 被过滤的字段从不格式化。
func (c *loggerCache) render(fields []any, freeze, filterEmpty bool, texts []any) []any {
	var buffer [32]fieldPair
	kept := buffer[:0]
	for i := 0; i < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok && c.filter.drops(key) {
			continue
		}
		value := slotMissing
		if i+1 < len(fields) {
			value = i + 1
		}
		kept = append(kept, fieldPair{key: i, value: value})
	}
	// moduleValue 复用已装箱的值，避免每条日志重新分配。
	moduleValue := c.module
	if moduleValue == nil {
		moduleValue = unknownModule
		for _, pair := range kept {
			if key, ok := fields[pair.key].(string); ok && key == moduleKey && pair.value != slotMissing {
				if value, ok := fields[pair.value].(string); ok && value != "" && strings.TrimSpace(value) == value {
					moduleValue = fields[pair.value]
				}
			}
		}
	}

	event := make([]any, 0, 2*len(kept)+2)
	emit := func(key, value any) {
		if filterEmpty && isEmptyValue(value) {
			return
		}
		// 只有 string 类型的 key 参与去重；formattedKey 等非字符串 key 原样保留。
		if name, ok := key.(string); ok {
			for j := 0; j < len(event); j += 2 {
				if existing, ok := event[j].(string); ok && existing == name {
					event[j+1] = value
					return
				}
			}
		}
		event = append(event, key, value)
	}
	emitPair := func(pair fieldPair) {
		key := c.text(fields, pair.key, true, freeze, texts)
		var value any = "(MISSING)"
		if pair.value != slotMissing {
			value = c.text(fields, pair.value, false, freeze, texts)
		}
		emit(key, value)
	}
	for _, group := range [...]string{TsKey, moduleKey, CallerKey} {
		if group == moduleKey {
			emit(moduleKey, moduleValue)
			continue
		}
		for _, pair := range kept {
			if key, _ := fields[pair.key].(string); key == group {
				emitPair(pair)
			}
		}
	}
	for _, pair := range kept {
		if key, _ := fields[pair.key].(string); key != TsKey && key != moduleKey && key != CallerKey {
			emitPair(pair)
		}
	}
	return event
}

// text 返回下标 index 处字段在输出中的表示。外部输出保留原始值；内置输出中字符串原样保留，
// key 位置的其他值转为 formattedKey，value 位置的 nil 保持 nil，其余值按原始下标的角色
// （偶数下标为 key 用 %s，奇数为 value 用 %v）格式化一次并缓存。
func (c *loggerCache) text(fields []any, index int, keyPosition, freeze bool, texts []any) any {
	raw := fields[index]
	if !freeze {
		return raw
	}
	// 直接返回原接口值，避免字符串重新装箱分配。
	if _, ok := raw.(string); ok {
		return raw
	}
	if raw == nil {
		if keyPosition {
			return formattedKey(fmt.Sprintf("%s", raw))
		}
		return nil
	}
	if texts[index] == nil {
		format := "%v"
		if index%2 == 0 {
			format = "%s"
		}
		texts[index] = fmt.Sprintf(format, raw)
	}
	if keyPosition {
		return formattedKey(texts[index].(string))
	}
	return texts[index]
}

// isEmptyValue 判断 nil 或格式化为空字符串的值；不移除数字 0 和 false。
func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	if s, ok := value.(string); ok {
		return s == ""
	}
	return fmt.Sprint(value) == ""
}
