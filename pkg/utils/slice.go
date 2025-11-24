package utils

// Select 选择第一个非0值
func Select[T comparable](vals ...T) T {
	var zero T
	for _, v := range vals {
		if v != zero {
			return v
		}
	}
	return zero
}

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

func Map[T, Y any](input []T, m func(T) Y) []Y {
	ret := make([]Y, 0, len(input))
	for i := range input {
		ret = append(ret, m(input[i]))
	}
	return ret
}

func MapTo[T, Y any](input []T, v Y) []Y {
	l := len(input)
	ret := make([]Y, l)
	for i := 0; i < l; i++ {
		ret[i] = v
	}
	return ret
}

func MapToAny[T any](input []T) []any {
	ret := make([]any, 0, len(input))
	for i := range input {
		ret = append(ret, input[i])
	}
	return ret
}

func Each[T any](input []T, m func(T)) []T {
	for i := range input {
		m(input[i])
	}
	return input
}

func Filter[T any](input []T, m func(T) bool) []T {
	ret := make([]T, 0, len(input))
	for i := range input {
		if m(input[i]) {
			ret = append(ret, input[i])
		}
	}
	return ret
}

func FilterZero[T comparable](input []T) []T {
	var zero T
	return Filter(input, func(t T) bool {
		return t != zero
	})
}

func Includes[T comparable](input []T, v T) bool {
	for i := range input {
		if input[i] == v {
			return true
		}
	}
	return false
}

func Find[T any](input []T, f func(T) bool) int {
	for i := range input {
		if f(input[i]) {
			return i
		}
	}
	return -1
}

func FindItem[T any](input []T, f func(T) bool) T {
	var zero T
	for i := range input {
		if f(input[i]) {
			return input[i]
		}
	}
	return zero
}

type GroupItem[GroupKey comparable, ValueType any] struct {
	Group  GroupKey
	Values []ValueType
}

type GroupItems[GroupKey comparable, ValueType any] []*GroupItem[GroupKey, ValueType]

func (g GroupItems[GroupKey, ValueType]) ToMap() map[GroupKey][]ValueType {
	m := make(map[GroupKey][]ValueType, len(g))
	for _, item := range g {
		m[item.Group] = item.Values
	}
	return m
}

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

func Pluck[T any, K comparable, V any](input []T, keyBy func(T) K, valueOf func(T) V) (m map[K]V) {
	m = make(map[K]V, len(input))
	for _, t := range input {
		m[keyBy(t)] = valueOf(t)
	}
	return m
}

func KeyBy[T any, K comparable](input []T, keyBy func(T) K) (m map[K]T) {
	m = make(map[K]T, len(input))
	for _, t := range input {
		m[keyBy(t)] = t
	}
	return m
}

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
