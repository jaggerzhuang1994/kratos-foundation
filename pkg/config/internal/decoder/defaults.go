package decoder

import (
	"reflect"
	"strings"
	"unicode"
)

// applyDefaults 为配置中缺失的键补入默认值，并按目标类型匹配结构体字段别名。
// 配置中已有的零值、空数组和显式 null 优先；map 键精确匹配，双方输入均不修改。
func applyDefaults(configValue, defaultValue any, target reflect.Type) any {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	defaultMap, defaultOK := defaultValue.(map[string]any)
	configMap, configOK := configValue.(map[string]any)
	if !defaultOK || !configOK {
		return clone(configValue)
	}

	result := clone(defaultMap).(map[string]any)
	defaultKeys := make(map[string]string, len(result))
	// 只有结构体字段支持命名别名；map 的键是业务数据，必须精确匹配。
	if target != nil && target.Kind() == reflect.Struct {
		for key := range result {
			defaultKeys[normalizedKey(key)] = key
		}
	}
	for configKey, configured := range configMap {
		targetKey := configKey
		if defaultKey, ok := defaultKeys[normalizedKey(configKey)]; ok {
			targetKey = defaultKey
		}
		if current, ok := result[targetKey]; ok {
			result[targetKey] = applyDefaults(configured, current, childType(target, targetKey))
		} else {
			result[targetKey] = clone(configured)
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
