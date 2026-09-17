package utils

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
)

// CloneJSON 使用 JSON 表示深复制仅由可序列化字段组成的值。
func CloneJSON[T any](source *T) (*T, error) {
	data, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EqualJSON 按 JSON 数据模型比较两个值，忽略对象成员顺序。
func EqualJSON(left, right any) (bool, error) {
	decode := func(value any) (any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var result any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	leftValue, err := decode(left)
	if err != nil {
		return false, err
	}
	rightValue, err := decode(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}

func CopyIntP(i *int) *int {
	if i == nil {
		return nil
	}
	value := *i
	return &value
}

func CopyFloat64P(f *float64) *float64 {
	if f == nil {
		return nil
	}
	value := *f
	return &value
}

func CopyBoolP(b *bool) *bool {
	if b == nil {
		return nil
	}
	value := *b
	return &value
}

func CopyAnyP(a *any) *any {
	if a == nil {
		return nil
	}
	value := *a
	return &value
}

func CopyStringArray(slice []string) []string {
	return slices.Clone(slice)
}

func CopyAnyArray(slice []any) []any {
	return slices.Clone(slice)
}

func CopyMapAny(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	dst := make(map[string]any, len(m))
	maps.Copy(dst, m)
	return dst
}

func CopyMapString(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	dst := make(map[string]string, len(m))
	maps.Copy(dst, m)
	return dst
}

func CopyMapStringArray(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	dst := make(map[string][]string, len(m))
	for k, v := range m {
		dst[k] = CopyStringArray(v)
	}
	return dst
}

func UInt32(i uint32) *int {
	ii := int(i)
	return &ii
}

func Int32(i int32) *int {
	ii := int(i)
	return &ii
}

func Int(i int) *int {
	return &i
}

func Bool(b bool) *bool {
	return &b
}
