package env

import "github.com/go-kratos/kratos/v2/log"

const (
	Local = "local" // 本地环境
	Dev   = "dev"   // 开发环境
	Test  = "test"  // 测试环境
	Pre   = "pre"   // 预发布环境
	Prod  = "prod"  // 正式环境
)

var appEnv string

var appEnvKeys = []string{
	"APP_ENV",
	"KRATOS_ENV",
}

func init() {
	appEnv = GetEnvKeys(appEnvKeys)

	switch appEnv {
	case Local, Dev, Test, Pre, Prod:
	default:
		log.Warnf("unknown environment=%s, set to local", appEnv)
		appEnv = Local // 无效env，设置为local
	}
}

func AppEnv() string {
	return appEnv
}

func IsLocal() bool { return appEnv == Local }

// IsDev 是否为开发环境
func IsDev() bool {
	return appEnv == Dev
}

// IsTest 是否为测试环境
func IsTest() bool {
	return appEnv == Test
}

// IsPre 是否为预发布
func IsPre() bool {
	return appEnv == Pre
}

// IsProd 是否为正式环境
func IsProd() bool {
	return appEnv == Prod
}

// IsOffline 是否为线下环境
func IsOffline() bool {
	return !IsOnline()
}

// IsOnline 是否为线上环境
func IsOnline() bool {
	return IsProd() || IsPre()
}
