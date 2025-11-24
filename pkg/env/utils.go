package env

import (
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

var stringParser = func(s string) (string, error) {
	return s, nil
}
var boolParser = strconv.ParseBool
var durationParser = time.ParseDuration
var intParser = strconv.Atoi

// GetEnv 获取key默认值
func GetEnv(key string, optionalDefaultValue ...string) string {
	return getEnvAs([]string{key}, stringParser, optionalDefaultValue)
}

// GetEnvKeys 从多个keys获取env值
func GetEnvKeys(keys []string, optionalDefaultValue ...string) string {
	return getEnvAs(keys, stringParser, optionalDefaultValue)
}

// GetEnvAsBool 从env获取bool值
func GetEnvAsBool(key string, optionalDefaultValue ...bool) (r bool) {
	return getEnvAs([]string{key}, boolParser, optionalDefaultValue)
}

// GetEnvKeysAsBool 从env获取bool值
func GetEnvKeysAsBool(key []string, optionalDefaultValue ...bool) (r bool) {
	return getEnvAs(key, boolParser, optionalDefaultValue)
}

// GetEnvAsDuration 从env获取时间间隔
func GetEnvAsDuration(key string, optionalDefaultValue ...time.Duration) (d time.Duration) {
	return getEnvAs([]string{key}, durationParser, optionalDefaultValue)
}

// GetEnvKeysAsDuration 从env获取时间间隔
func GetEnvKeysAsDuration(key []string, optionalDefaultValue ...time.Duration) (d time.Duration) {
	return getEnvAs(key, durationParser, optionalDefaultValue)
}

// GetEnvAsInt 从env获取int
func GetEnvAsInt(key string, optionalDefaultValue ...int) (d int) {
	return getEnvAs([]string{key}, intParser, optionalDefaultValue)
}

// GetEnvKeysAsInt 从env获取int
func GetEnvKeysAsInt(key []string, optionalDefaultValue ...int) (d int) {
	return getEnvAs(key, intParser, optionalDefaultValue)
}

// .
func getEnvAs[T any](keys []string, parser func(string) (T, error), optionalDefaultValue []T) (r T) {
	// 遍历keys，找到第一个设置值的key
	for _, key := range keys {
		v := os.Getenv(key)
		var err error
		if v != "" {
			r, err = parser(v)
			if err != nil {
				panic(errors.WithMessagef(err, "get env as %T failed", r)) // 读取env中有值，但是解析失败，则报错
			}
			return
		}
	}
	// 如果keys都没值，则从默认值中取一个
	if len(optionalDefaultValue) > 0 {
		r = optionalDefaultValue[0]
		return
	}
	// 如果没指定默认值，则返回T的0值
	return
}
