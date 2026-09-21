package utils

import "slices"

// Unique 按首次出现顺序返回去重后的新切片。
func Unique[T comparable](input []T) []T {
	ret := make([]T, 0, len(input))
	m := map[T]struct{}{}
	for i := range input {
		if _, exists := m[input[i]]; !exists {
			m[input[i]] = struct{}{}
			ret = append(ret, input[i])
		}
	}
	return ret
}

// Map 按输入顺序映射元素，返回新切片。
func Map[T, Y any](input []T, m func(T) Y) []Y {
	ret := make([]Y, 0, len(input))
	for i := range input {
		ret = append(ret, m(input[i]))
	}
	return ret
}

// MapTo 返回与输入等长且元素均为 v 的新切片。
func MapTo[T, Y any](input []T, v Y) []Y {
	l := len(input)
	ret := make([]Y, l)
	for i := range l {
		ret[i] = v
	}
	return ret
}

// MapToAny 按输入顺序将元素转换为 any，返回新切片。
func MapToAny[T any](input []T) []any {
	ret := make([]any, 0, len(input))
	for i := range input {
		ret = append(ret, input[i])
	}
	return ret
}

// Each 按输入顺序调用回调，并返回原切片。
func Each[T any](input []T, m func(T)) []T {
	for i := range input {
		m(input[i])
	}
	return input
}

// Filter 按输入顺序返回匹配元素的新切片。
func Filter[T any](input []T, m func(T) bool) []T {
	ret := make([]T, 0, len(input))
	for i := range input {
		if m(input[i]) {
			ret = append(ret, input[i])
		}
	}
	return ret
}

// FilterZero 按输入顺序返回非零元素的新切片。
func FilterZero[T comparable](input []T) []T {
	var zero T
	return Filter(input, func(t T) bool {
		return t != zero
	})
}

// Includes 报告切片是否包含指定值。
//
// Deprecated: 使用 slices.Contains。
func Includes[T comparable](input []T, v T) bool {
	return slices.Contains(input, v)
}

// Find 返回首个匹配元素的索引；未找到时返回 -1。
//
// Deprecated: 使用 slices.IndexFunc。
func Find[T any](input []T, f func(T) bool) int {
	for i := range input {
		if f(input[i]) {
			return i
		}
	}
	return -1
}

// FindItem 返回首个匹配元素；未找到时返回元素类型的零值。
func FindItem[T any](input []T, f func(T) bool) T {
	var zero T
	for i := range input {
		if f(input[i]) {
			return input[i]
		}
	}
	return zero
}

// GroupItem 保存一个分组键及按输入顺序排列的组内元素。
type GroupItem[GroupKey comparable, ValueType any] struct {
	Group  GroupKey
	Values []ValueType
}

// GroupItems 按分组键首次出现的顺序保存各分组。
type GroupItems[GroupKey comparable, ValueType any] []*GroupItem[GroupKey, ValueType]

// ToMap 返回分组映射；值切片与原分组共享存储，分组指针必须非 nil。
func (g GroupItems[GroupKey, ValueType]) ToMap() map[GroupKey][]ValueType {
	m := make(map[GroupKey][]ValueType, len(g))
	for _, item := range g {
		m[item.Group] = item.Values
	}
	return m
}

// GroupBy 按分组键首次出现的顺序分组，保留组内元素的输入顺序。
func GroupBy[GroupKey comparable, ValueType any](input []ValueType, group func(ValueType) GroupKey) (ret GroupItems[GroupKey, ValueType]) {
	m := map[GroupKey]*GroupItem[GroupKey, ValueType]{}
	for i := range input {
		g := group(input[i])
		groupItem, exists := m[g]
		if !exists {
			m[g] = &GroupItem[GroupKey, ValueType]{Group: g}
			groupItem = m[g]
			ret = append(ret, groupItem)
		}
		groupItem.Values = append(groupItem.Values, input[i])
	}
	return ret
}

// Pluck 按输入元素生成键值映射；重复键由后一个元素覆盖。
func Pluck[T any, K comparable, V any](input []T, keyBy func(T) K, valueOf func(T) V) (m map[K]V) {
	m = make(map[K]V, len(input))
	for _, t := range input {
		m[keyBy(t)] = valueOf(t)
	}
	return m
}

// KeyBy 按输入元素生成键映射；重复键由后一个元素覆盖。
func KeyBy[T any, K comparable](input []T, keyBy func(T) K) (m map[K]T) {
	m = make(map[K]T, len(input))
	for _, t := range input {
		m[keyBy(t)] = t
	}
	return m
}

// Intersect 按 b 中的出现顺序返回去重交集；无交集时返回 nil。
func Intersect[T comparable](a, b []T) []T {
	m := make(map[T]struct{})
	for _, item := range a {
		m[item] = struct{}{}
	}

	var intersection []T
	for _, item := range b {
		if _, exists := m[item]; exists {
			intersection = append(intersection, item)
			delete(m, item)
		}
	}
	return intersection
}

// UniqueBy 根据 keyBy 返回的键去重，保留每个键首次出现的元素和输入顺序。
// 返回新切片，空结果非 nil；元素按值浅复制。
func UniqueBy[T any, K comparable](input []T, keyBy func(T) K) []T {
	seen := make(map[K]struct{}, len(input))
	result := make([]T, 0, len(input))
	for _, value := range input {
		key := keyBy(value)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

// FilterMap 按输入顺序调用 transform，仅收集返回 true 的映射值。
// 返回新切片，空结果非 nil；回调对每个元素仅执行一次。
func FilterMap[T, Y any](input []T, transform func(T) (Y, bool)) []Y {
	result := make([]Y, 0, len(input))
	for _, value := range input {
		if mapped, keep := transform(value); keep {
			result = append(result, mapped)
		}
	}
	return result
}

// Partition 将输入分成匹配与不匹配的两个新切片，各自保留输入顺序。
// 空结果非 nil；元素按值浅复制，predicate 对每个元素仅执行一次。
func Partition[T any](input []T, predicate func(T) bool) (matched, rest []T) {
	matched, rest = make([]T, 0), make([]T, 0)
	for _, value := range input {
		if predicate(value) {
			matched = append(matched, value)
		} else {
			rest = append(rest, value)
		}
	}
	return matched, rest
}

// Difference 返回 a 中存在而 b 中不存在的去重元素，保留其在 a 中首次出现的顺序。
// 返回新切片，空结果非 nil；比较沿用 Go 的 comparable 语义。
func Difference[T comparable](a, b []T) []T {
	excluded := make(map[T]struct{}, len(b))
	for _, value := range b {
		excluded[value] = struct{}{}
	}
	result := make([]T, 0, len(a))
	for _, value := range a {
		if _, skip := excluded[value]; !skip {
			result = append(result, value)
			// 输出过的元素也加入排除集合，保持与 Intersect 一致的集合去重语义。
			excluded[value] = struct{}{}
		}
	}
	return result
}
