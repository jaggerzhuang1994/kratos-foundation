package utils

import "maps"

// MValues 返回新切片中的全部值，顺序不确定。
//
// Deprecated: 使用 maps.Values 遍历，或 slices.Collect(maps.Values(m)) 收集；空输入时后者返回 nil。
func MValues[K comparable, T any](m map[K]T) []T {
	ret := make([]T, 0, len(m))
	for _, t := range m {
		ret = append(ret, t)
	}
	return ret
}

// MKeys 返回新切片中的全部键，顺序不确定。
//
// Deprecated: 使用 maps.Keys 遍历，或 slices.Collect(maps.Keys(m)) 收集；空输入时后者返回 nil。
func MKeys[K comparable, T any](m map[K]T) []K {
	ret := make([]K, 0, len(m))
	for k := range m {
		ret = append(ret, k)
	}
	return ret
}

// MMap 保留键并映射各值，返回新的 map。
func MMap[T, Y any, K comparable](input map[K]T, m func(T) Y) map[K]Y {
	ret := make(map[K]Y, len(input))
	for i := range input {
		ret[i] = m(input[i])
	}
	return ret
}

// MMapTo 保留键并将每个值设为 v，返回新的 map。
func MMapTo[T, Y any, K comparable](input map[K]T, v Y) map[K]Y {
	ret := make(map[K]Y, len(input))
	for k := range input {
		ret[k] = v
	}
	return ret
}

// MMapToAny 保留键并将值转换为 any，返回新的 map。
func MMapToAny[T any, K comparable](input map[K]T) map[K]any {
	ret := make(map[K]any, len(input))
	for i := range input {
		ret[i] = input[i]
	}
	return ret
}

// MEach 遍历各值并返回原 map；遍历顺序不确定。
func MEach[T any, K comparable](input map[K]T, m func(T)) map[K]T {
	for i := range input {
		m(input[i])
	}
	return input
}

// MFilter 返回包含所有匹配值的新 map。
func MFilter[T any, K comparable](input map[K]T, m func(T) bool) map[K]T {
	ret := make(map[K]T, len(input))
	for i := range input {
		if m(input[i]) {
			ret[i] = input[i]
		}
	}
	return ret
}

// MFilterZero 返回移除零值后的新 map。
func MFilterZero[T, K comparable](input map[K]T) map[K]T {
	var zero T
	return MFilter(input, func(t T) bool {
		return t != zero
	})
}

// MIncludes 报告 map 是否包含指定值。
func MIncludes[T, K comparable](input map[K]T, v T) bool {
	for i := range input {
		if input[i] == v {
			return true
		}
	}
	return false
}

// MFind 返回任意匹配值的键；未找到时返回 false 和键的零值。
func MFind[T any, K comparable](input map[K]T, f func(T) bool) (bool, K) {
	for i := range input {
		if f(input[i]) {
			return true, i
		}
	}
	var zeroK K
	return false, zeroK
}

// MKeyBy 按值生成键；重复键保留遍历中最后一个值，遍历顺序不确定。
func MKeyBy[T any, K, K2 comparable](input map[K]T, keyBy func(T) K2) (m map[K2]T) {
	m = make(map[K2]T, len(input))
	for _, t := range input {
		m[keyBy(t)] = t
	}
	return m
}

// Clone 浅复制 map；nil 输入返回非 nil 空 map。
//
// Deprecated: 使用 maps.Clone；注意 maps.Clone 对 nil 输入仍返回 nil。
func Clone[K comparable, V any](m map[K]V) map[K]V {
	mm := make(map[K]V, len(m))
	maps.Copy(mm, m)
	return mm
}

// MPick 浅复制指定键中实际存在的条目，返回新的 map；缺失键跳过，空结果非 nil。
func MPick[K comparable, V any](input map[K]V, keys ...K) map[K]V {
	result := make(map[K]V, min(len(input), len(keys)))
	for _, key := range keys {
		// 存在但值为零的条目也应保留，不能通过值判断是否存在。
		if value, ok := input[key]; ok {
			result[key] = value
		}
	}
	return result
}

// MOmit 浅复制除指定键以外的条目，返回新的 map；缺失键忽略，空结果非 nil。
func MOmit[K comparable, V any](input map[K]V, keys ...K) map[K]V {
	excluded := make(map[K]struct{}, len(keys))
	for _, key := range keys {
		excluded[key] = struct{}{}
	}
	result := make(map[K]V, len(input))
	for key, value := range input {
		if _, skip := excluded[key]; !skip {
			result[key] = value
		}
	}
	return result
}
