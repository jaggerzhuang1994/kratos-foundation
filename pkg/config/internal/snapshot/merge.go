package snapshot

func merge(baseValue, overrideValue any) any {
	baseMap, baseOK := baseValue.(map[string]any)
	overrideMap, overrideOK := overrideValue.(map[string]any)
	if !baseOK || !overrideOK {
		return clone(overrideValue)
	}

	result := clone(baseMap).(map[string]any)
	for key, override := range overrideMap {
		if current, ok := result[key]; ok {
			result[key] = merge(current, override)
		} else {
			result[key] = clone(override)
		}
	}
	return result
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
