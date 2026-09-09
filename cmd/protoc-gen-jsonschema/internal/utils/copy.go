package utils

import (
	"maps"
	"slices"
)

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
