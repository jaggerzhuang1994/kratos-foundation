package decoder

import (
	"reflect"
	"strings"
	"unicode"
)

func merge(baseValue, overrideValue any, target reflect.Type) any {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	baseMap, baseOK := baseValue.(map[string]any)
	overrideMap, overrideOK := overrideValue.(map[string]any)
	if !baseOK || !overrideOK {
		return clone(overrideValue)
	}

	result := clone(baseMap).(map[string]any)
	baseKeys := make(map[string]string, len(result))
	// 只有结构体字段支持命名别名；map 的键是业务数据，必须精确匹配。
	if target != nil && target.Kind() == reflect.Struct {
		for key := range result {
			baseKeys[normalizedKey(key)] = key
		}
	}
	for overrideKey, override := range overrideMap {
		targetKey := overrideKey
		if baseKey, ok := baseKeys[normalizedKey(overrideKey)]; ok {
			targetKey = baseKey
		}
		if current, ok := result[targetKey]; ok {
			result[targetKey] = merge(current, override, childType(target, targetKey))
		} else {
			result[targetKey] = clone(override)
		}
	}
	return result
}

// childType 沿目标类型识别嵌套 map，避免字段别名规则泄漏到字典键。
func childType(target reflect.Type, key string) reflect.Type {
	if target == nil {
		return nil
	}
	if target.Kind() == reflect.Map {
		return target.Elem()
	}
	if target.Kind() != reflect.Struct {
		return nil
	}
	for _, field := range reflect.VisibleFields(target) {
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if normalizedKey(name) == normalizedKey(key) {
			return field.Type
		}
	}
	return nil
}

func clone(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = clone(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = clone(item)
		}
		return result
	default:
		return value
	}
}

func normalizedKey(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character == '_' || character == '-' || unicode.IsSpace(character) {
			continue
		}
		builder.WriteRune(unicode.ToLower(character))
	}
	return builder.String()
}
