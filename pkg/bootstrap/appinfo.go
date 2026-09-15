package bootstrap

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// AppInfoBootstrap 标记 appinfo 的组装贡献已完成。
type AppInfoBootstrap struct{}

// NewAppInfoBootstrap 将已有组件接入应用组装，不启动运行时。
func NewAppInfoBootstrap(spec *app.Spec, info appinfo.AppInfo) (AppInfoBootstrap, error) {
	if err := spec.RegisterAppInfo(info); err != nil {
		return AppInfoBootstrap{}, fmt.Errorf("appinfo bootstrap: register app info: %w", err)
	}
	log.RegisterFields(
		log.ServiceIDKey, info.ID(),
		log.ServiceNameKey, info.Name(),
		log.ServiceVersionKey, info.Version(),
	)
	return AppInfoBootstrap{}, nil
}
