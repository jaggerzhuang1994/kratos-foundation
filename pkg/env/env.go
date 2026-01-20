package env

import (
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

const (
	Local = "local" // 本地环境
	Dev   = "dev"   // 开发环境
	Test  = "test"  // 测试环境
	Pre   = "pre"   // 预发布环境
	Prod  = "prod"  // 正式环境
)

var env string
var debug bool

var appEnvKeys = []string{
	"APP_ENV",
	"KRATOS_ENV",
}

var appDebugKeys = []string{
	"APP_DEBUG",
	"KRATOS_DEBUG",
}

func init() {
	env = getEnv(appEnvKeys, Local)
	switch env {
	case Local, Dev, Test, Pre, Prod:
	default:
		// 无效env，设置为local
		log.Warnf("unknown env: %s, set to local", env)
		env = Local
	}
	debug = getEnvAsBool(appDebugKeys)
}

func AppEnv() string {
	return env
}

func AppDebug() bool {
	return debug
}

func IsLocal() bool {
	return env == Local
}

func IsDev() bool {
	return env == Dev
}

func IsTest() bool {
	return env == Test
}

func IsPre() bool {
	return env == Pre
}

func IsProd() bool {
	return env == Prod
}

func IsOffline() bool {
	return !IsOnline()
}

func IsOnline() bool {
	return IsPre() || IsProd()
}

func GetEnv(key string, optionalDefaultValue ...string) string {
	return getEnv([]string{key}, optionalDefaultValue...)
}

func GetEnv2(keys []string, optionalDefaultValue ...string) string {
	return getEnv(keys, optionalDefaultValue...)
}

func GetEnvAsBool(key string, optionalDefaultValue ...bool) (r bool) {
	return getEnvAsBool([]string{key}, optionalDefaultValue...)
}

func GetEnvAsBool2(keys []string, optionalDefaultValue ...bool) (r bool) {
	return getEnvAsBool(keys, optionalDefaultValue...)
}

func GetEnvAsDuration(key string, optionalDefaultValue ...time.Duration) (d time.Duration) {
	return getEnvAsDuration([]string{key}, optionalDefaultValue...)
}

func GetEnvAsDuration2(keys []string, optionalDefaultValue ...time.Duration) (d time.Duration) {
	return getEnvAsDuration(keys, optionalDefaultValue...)
}

func GetEnvAsInt(key string, optionalDefaultValue ...int) (d int) {
	return getEnvAsInt([]string{key}, optionalDefaultValue...)
}

func GetEnvAsInt2(keys []string, optionalDefaultValue ...int) (d int) {
	return getEnvAsInt(keys, optionalDefaultValue...)
}
