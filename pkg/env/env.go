package env

import (
	"fmt"
	"os"
	"strconv"
)

const (
	// Local 表示本地开发环境。
	Local = "local"
	// Dev 表示共享开发环境。
	Dev = "dev"
	// Test 表示测试环境。
	Test = "test"
	// Pre 表示预发布环境。
	Pre = "pre"
	// Prod 表示生产环境。
	Prod = "prod"
)

// AppEnv 返回校验后的应用环境；未配置时默认 local，显式非法值会立即 panic。
func AppEnv() string {
	value := getEnv([]string{"APP_ENV", "KRATOS_ENV"}, Local)
	switch value {
	case Local, Dev, Test, Pre, Prod:
		return value
	default:
		panic(fmt.Sprintf(
			"invalid application environment %q: want one of %q, %q, %q, %q, %q",
			value,
			Local,
			Dev,
			Test,
			Pre,
			Prod,
		))
	}
}

// AppDebug 返回应用调试开关；显式非法布尔值会立即 panic，避免错误配置被静默忽略。
func AppDebug() bool {
	return getEnvAsBool([]string{"APP_DEBUG", "KRATOS_DEBUG"})
}

// IsLocal 判断应用是否处于本地开发环境。
func IsLocal() bool {
	return AppEnv() == Local
}

// IsDev 判断应用是否处于共享开发环境。
func IsDev() bool {
	return AppEnv() == Dev
}

// IsTest 判断应用是否处于测试环境。
func IsTest() bool {
	return AppEnv() == Test
}

// IsPre 判断应用是否处于预发布环境。
func IsPre() bool {
	return AppEnv() == Pre
}

// IsProd 判断应用是否处于生产环境。
func IsProd() bool {
	return AppEnv() == Prod
}

// IsOffline 判断应用是否处于非线上环境，即 local、dev 或 test。
func IsOffline() bool {
	return !IsOnline()
}

// IsOnline 判断应用是否处于线上环境，即 pre 或 prod。
func IsOnline() bool {
	return IsPre() || IsProd()
}

// GetEnv 返回指定环境变量，变量不存在时使用首个可选默认值。
func GetEnv(key string, optionalDefaultValue ...string) string {
	return getEnv([]string{key}, optionalDefaultValue...)
}

// GetEnvAsBool 解析布尔环境变量，变量不存在时使用首个默认值；非法值会立即 panic。
func GetEnvAsBool(key string, optionalDefaultValue ...bool) bool {
	return getEnvAsBool([]string{key}, optionalDefaultValue...)
}

// getEnv 按顺序读取第一个存在的字符串环境变量。
func getEnv(keys []string, optionalDefaultValue ...string) string {
	return getEnvAs(keys, func(value string) (string, error) {
		return value, nil
	}, optionalDefaultValue...)
}

// getEnvAsBool 按顺序读取并解析第一个存在的布尔环境变量。
func getEnvAsBool(keys []string, optionalDefaultValue ...bool) bool {
	return getEnvAs(keys, strconv.ParseBool, optionalDefaultValue...)
}

// getEnvAs 统一实现多键优先级、类型转换与默认值规则。
func getEnvAs[T any](keys []string, parser func(string) (T, error), optionalDefaultValue ...T) (r T) {
	var err error
	for _, key := range keys {
		v, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		r, err = parser(v)
		if err != nil {
			panic(fmt.Errorf("parse environment variable %q as %T: %w", key, r, err))
		}
		return
	}

	if len(optionalDefaultValue) > 0 {
		r = optionalDefaultValue[0]
	}
	return
}
