package gormscope

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"time"
)

type sortKey struct {
	name      string
	direction bool
	kind      sortKind
}

type sortKind uint8

const (
	timeKind sortKind = iota
	numberKind
	stringKind
)

// field 返回排序字段名。
func (key sortKey) field() string { return key.name }

// ascending 返回排序方向是否为升序。
func (key sortKey) ascending() bool { return key.direction }

// encode 按排序键类型编码模型值。
func (key sortKey) encode(value any) (string, error) {
	switch key.kind {
	case timeKind:
		// 必须保留纳秒：截断到整秒会让同一秒内的多行落在游标条件两侧之外，
		// 降序分页漏行、升序分页重复返回上一页。
		switch typed := value.(type) {
		case time.Time:
			return strconv.FormatInt(typed.UnixNano(), 10), nil
		case *time.Time:
			if typed != nil {
				return strconv.FormatInt(typed.UnixNano(), 10), nil
			}
		}
		return "", fmt.Errorf("expected time.Time, got %T", value)
	case numberKind:
		return encodeNumber(value)
	case stringKind:
		value, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("expected string, got %T", value)
		}
		return value, nil
	default:
		return "", errors.New("unknown sort-key kind")
	}
}

// decode 按排序键类型解码游标值。
func (key sortKey) decode(value string) (any, error) {
	switch key.kind {
	case timeKind:
		nanoseconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		// 固定用 UTC 还原：time.Unix 默认返回本地时区，会让按文本比较时间的驱动
		// （例如 SQLite）的游标边界随进程时区变化。
		return time.Unix(0, nanoseconds).UTC(), nil
	case numberKind:
		if _, ok := new(big.Rat).SetString(value); !ok {
			return nil, errors.New("invalid number")
		}
		return value, nil
	case stringKind:
		return value, nil
	default:
		return nil, errors.New("unknown sort-key kind")
	}
}

// encodeNumber 无精度损失地编码常用数值和 Stringer 数值。
func encodeNumber(value any) (string, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return "", errors.New("number is nil")
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(reflected.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(reflected.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(reflected.Float(), 'g', -1, reflected.Type().Bits()), nil
	case reflect.String:
		text := reflected.String()
		if _, ok := new(big.Rat).SetString(text); ok {
			return text, nil
		}
	}
	if stringer, ok := value.(fmt.Stringer); ok {
		text := stringer.String()
		if _, valid := new(big.Rat).SetString(text); valid {
			return text, nil
		}
	}
	return "", fmt.Errorf("expected numeric value, got %T", value)
}

type TimeAscSortKey string

type TimeDescSortKey string

type NumberAscSortKey string

type NumberDescSortKey string

type StringAscSortKey string

type StringDescSortKey string

// implementation 返回升序时间键的统一实现。
func (key TimeAscSortKey) implementation() sortKey { return sortKey{string(key), true, timeKind} }

// implementation 返回降序时间键的统一实现。
func (key TimeDescSortKey) implementation() sortKey { return sortKey{string(key), false, timeKind} }

// implementation 返回升序数值键的统一实现。
func (key NumberAscSortKey) implementation() sortKey {
	return sortKey{string(key), true, numberKind}
}

// implementation 返回降序数值键的统一实现。
func (key NumberDescSortKey) implementation() sortKey {
	return sortKey{string(key), false, numberKind}
}

// implementation 返回升序字符串键的统一实现。
func (key StringAscSortKey) implementation() sortKey {
	return sortKey{string(key), true, stringKind}
}

// implementation 返回降序字符串键的统一实现。
func (key StringDescSortKey) implementation() sortKey {
	return sortKey{string(key), false, stringKind}
}

// field 返回升序时间字段名。
func (key TimeAscSortKey) field() string { return key.implementation().field() }

// ascending 表示时间键按升序排列。
func (key TimeAscSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码升序时间游标值。
func (key TimeAscSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码升序时间游标值。
func (key TimeAscSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}

// field 返回降序时间字段名。
func (key TimeDescSortKey) field() string { return key.implementation().field() }

// ascending 表示时间键按降序排列。
func (key TimeDescSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码降序时间游标值。
func (key TimeDescSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码降序时间游标值。
func (key TimeDescSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}

// field 返回升序数值字段名。
func (key NumberAscSortKey) field() string { return key.implementation().field() }

// ascending 表示数值键按升序排列。
func (key NumberAscSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码升序数值游标值。
func (key NumberAscSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码升序数值游标值。
func (key NumberAscSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}

// field 返回降序数值字段名。
func (key NumberDescSortKey) field() string { return key.implementation().field() }

// ascending 表示数值键按降序排列。
func (key NumberDescSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码降序数值游标值。
func (key NumberDescSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码降序数值游标值。
func (key NumberDescSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}

// field 返回升序字符串字段名。
func (key StringAscSortKey) field() string { return key.implementation().field() }

// ascending 表示字符串键按升序排列。
func (key StringAscSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码升序字符串游标值。
func (key StringAscSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码升序字符串游标值。
func (key StringAscSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}

// field 返回降序字符串字段名。
func (key StringDescSortKey) field() string { return key.implementation().field() }

// ascending 表示字符串键按降序排列。
func (key StringDescSortKey) ascending() bool { return key.implementation().ascending() }

// encode 编码降序字符串游标值。
func (key StringDescSortKey) encode(value any) (string, error) {
	return key.implementation().encode(value)
}

// decode 解码降序字符串游标值。
func (key StringDescSortKey) decode(value string) (any, error) {
	return key.implementation().decode(value)
}
