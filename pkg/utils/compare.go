package utils

type ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64 | ~string
}

func Max[T ordered](x T, y ...T) T {
	var r = x
	for _, t := range y {
		if t > r {
			r = t
		}
	}
	return r
}

func Min[T ordered](x T, y ...T) T {
	var r = x
	for _, t := range y {
		if t < r {
			r = t
		}
	}
	return r
}
