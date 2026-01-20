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

func getEnv(keys []string, optionalDefaultValue ...string) string {
	return getEnvAs(keys, stringParser, optionalDefaultValue...)
}

func getEnvAsBool(keys []string, optionalDefaultValue ...bool) bool {
	return getEnvAs(keys, boolParser, optionalDefaultValue...)
}

func getEnvAsDuration(keys []string, optionalDefaultValue ...time.Duration) time.Duration {
	return getEnvAs(keys, durationParser, optionalDefaultValue...)
}

func getEnvAsInt(keys []string, optionalDefaultValue ...int) int {
	return getEnvAs(keys, intParser, optionalDefaultValue...)
}

func getEnvAs[T any](keys []string, parser func(string) (T, error), optionalDefaultValue ...T) (r T) {
	var err error
	// 遍历keys，找到第一个设置值的key，并解析
	for _, key := range keys {
		v, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		r, err = parser(v)
		if err != nil {
			panic(errors.WithMessagef(err, "parse env[%s] as %T failed", key, r))
		}
		return
	}

	// 如果keys都没值，则从默认值中第一个
	if len(optionalDefaultValue) > 0 {
		r = optionalDefaultValue[0]
	}
	// 如果没有指定默认值，则返回零值
	return
}
