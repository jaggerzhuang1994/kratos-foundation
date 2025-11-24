package utils

func MValues[K comparable, T any](m map[K]T) []T {
	ret := make([]T, 0, len(m))
	for _, t := range m {
		ret = append(ret, t)
	}
	return ret
}

func MKeys[K comparable, T any](m map[K]T) []K {
	ret := make([]K, 0, len(m))
	for k := range m {
		ret = append(ret, k)
	}
	return ret
}

func MMap[T, Y any, K comparable](input map[K]T, m func(T) Y) map[K]Y {
	ret := make(map[K]Y, len(input))
	for i := range input {
		ret[i] = m(input[i])
	}
	return ret
}

func MMapTo[T, Y any, K comparable](input map[K]T, v Y) map[K]Y {
	ret := make(map[K]Y, len(input))
	for k := range input {
		ret[k] = v
	}
	return ret
}

func MMapToAny[T any, K comparable](input map[K]T) map[K]any {
	ret := make(map[K]any, len(input))
	for i := range input {
		ret[i] = input[i]
	}
	return ret
}

func MEach[T any, K comparable](input map[K]T, m func(T)) map[K]T {
	for i := range input {
		m(input[i])
	}
	return input
}

func MFilter[T any, K comparable](input map[K]T, m func(T) bool) map[K]T {
	ret := make(map[K]T, len(input))
	for i := range input {
		if m(input[i]) {
			ret[i] = input[i]
		}
	}
	return ret
}

func MFilterZero[T, K comparable](input map[K]T) map[K]T {
	var zero T
	return MFilter(input, func(t T) bool {
		return t != zero
	})
}

func MIncludes[T, K comparable](input map[K]T, v T) bool {
	for i := range input {
		if input[i] == v {
			return true
		}
	}
	return false
}

func MFind[T any, K comparable](input map[K]T, f func(T) bool) (bool, K) {
	for i := range input {
		if f(input[i]) {
			return true, i
		}
	}
	var zeroK K
	return false, zeroK
}

func MKeyBy[T any, K, K2 comparable](input map[K]T, keyBy func(T) K2) (m map[K2]T) {
	m = make(map[K2]T, len(input))
	for _, t := range input {
		m[keyBy(t)] = t
	}
	return m
}

func Clone[K comparable, V any](m map[K]V) map[K]V {
	mm := make(map[K]V, len(m))
	for k, v := range m {
		mm[k] = v
	}
	return mm
}
