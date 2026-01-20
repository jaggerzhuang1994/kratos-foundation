package utils

// Reverse 反转一个切片，返回新的切片
func Reverse[T any](input []T) []T {
	out := make([]T, len(input))
	for i := range input {
		out[len(input)-1-i] = input[i]
	}
	return out
}

// Select 从多个值中选择第一个非零值
// 如果所有值都是零值，则返回零值
func Select[T comparable](vals ...T) T {
	var zero T
	for _, v := range vals {
		if v != zero {
			return v
		}
	}
	return zero
}

// Unique 切片去重，保持原始顺序
// 使用 map 实现去重，适用于可比较的类型
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

// Map 对切片中的每个元素应用映射函数，返回新的切片
func Map[T, Y any](input []T, m func(T) Y) []Y {
	ret := make([]Y, 0, len(input))
	for i := range input {
		ret = append(ret, m(input[i]))
	}
	return ret
}

// MapTo 将切片中的每个元素映射为固定值，返回新的切片
func MapTo[T, Y any](input []T, v Y) []Y {
	l := len(input)
	ret := make([]Y, l)
	for i := 0; i < l; i++ {
		ret[i] = v
	}
	return ret
}

// MapToAny 将任意类型的切片转换为 []any 切片
func MapToAny[T any](input []T) []any {
	ret := make([]any, 0, len(input))
	for i := range input {
		ret = append(ret, input[i])
	}
	return ret
}

// Each 遍历切片并对每个元素执行函数，返回原切片
func Each[T any](input []T, m func(T)) []T {
	for i := range input {
		m(input[i])
	}
	return input
}

// Filter 过滤切片，保留满足条件的元素
func Filter[T any](input []T, m func(T) bool) []T {
	ret := make([]T, 0, len(input))
	for i := range input {
		if m(input[i]) {
			ret = append(ret, input[i])
		}
	}
	return ret
}

// FilterZero 过滤切片中的零值元素
func FilterZero[T comparable](input []T) []T {
	var zero T
	return Filter(input, func(t T) bool {
		return t != zero
	})
}

// Includes 判断切片是否包含某个值
func Includes[T comparable](input []T, v T) bool {
	for i := range input {
		if input[i] == v {
			return true
		}
	}
	return false
}

// Find 查找满足条件的第一个元素索引，不存在返回 -1
func Find[T any](input []T, f func(T) bool) int {
	for i := range input {
		if f(input[i]) {
			return i
		}
	}
	return -1
}

// FindItem 查找满足条件的第一个元素，不存在返回零值
func FindItem[T any](input []T, f func(T) bool) T {
	var zero T
	for i := range input {
		if f(input[i]) {
			return input[i]
		}
	}
	return zero
}

// GroupItem 分组项结构体，包含分组键和对应的值列表
type GroupItem[GroupKey comparable, ValueType any] struct {
	Group  GroupKey    // 分组键
	Values []ValueType // 该组的值列表
}

// GroupItems 分组项列表类型
type GroupItems[GroupKey comparable, ValueType any] []*GroupItem[GroupKey, ValueType]

// ToMap 将分组项转换为 map 格式
func (g GroupItems[GroupKey, ValueType]) ToMap() map[GroupKey][]ValueType {
	m := make(map[GroupKey][]ValueType, len(g))
	for _, item := range g {
		m[item.Group] = item.Values
	}
	return m
}

// GroupBy 按照指定的分组函数对切片进行分组
// 返回按分组键组织的分组项列表
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

// Pluck 从对象切片中提取键值对，构建为 map
// keyBy 函数用于提取键，valueOf 函数用于提取值
func Pluck[T any, K comparable, V any](input []T, keyBy func(T) K, valueOf func(T) V) (m map[K]V) {
	m = make(map[K]V, len(input))
	for _, t := range input {
		m[keyBy(t)] = valueOf(t)
	}
	return m
}

// KeyBy 从对象切片中提取键，构建为 map
// map 的键由 keyBy 函数提取，值为原始对象
func KeyBy[T any, K comparable](input []T, keyBy func(T) K) (m map[K]T) {
	m = make(map[K]T, len(input))
	for _, t := range input {
		m[keyBy(t)] = t
	}
	return m
}

// Intersect 计算两个切片的交集
// 返回在两个切片中都出现的元素，去重且保持顺序
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

// Flat 将二维切片拍平为一维切片
func Flat[T any](input [][]T) []T {
	var total int
	for _, arr := range input {
		total += len(arr)
	}

	out := make([]T, 0, total)
	for _, arr := range input {
		out = append(out, arr...)
	}

	return out
}

// CheckBool bool值的检验函数，直接返回输入值
// 可用于需要函数类型的场景
func CheckBool(t bool) bool {
	return t
}

// Every 检查切片中的所有元素是否都满足条件
// 只有所有元素都通过检查才返回 true
func Every[T any](input []T, check func(T) bool) bool {
	for _, item := range input {
		// 存在一个false，则返回false
		if !check(item) {
			return false
		}
	}
	return true
}

// Some 检查切片中是否存在至少一个元素满足条件
// 只要有一个元素通过检查就返回 true
func Some[T any](input []T, check func(T) bool) bool {
	for _, item := range input {
		// 存在一个true，则返回true
		if check(item) {
			return true
		}
	}
	return false
}
