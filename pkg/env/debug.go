package env

var appDebug bool

var appDebugKeys = []string{
	"APP_DEBUG",
	"KRATOS_DEBUG",
}

func init() {
	appDebug = GetEnvKeysAsBool(appDebugKeys, IsLocal())
}

func AppDebug() bool {
	return appDebug
}
