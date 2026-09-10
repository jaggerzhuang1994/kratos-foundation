package bootstrap

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

// NewStopPolicy 在组件组装完成后创建应用停机策略，自动校验服务器等待时间。
// 无服务器时等待时间为零；返回的订阅 cleanup 由 Wire 逆序释放。
func NewStopPolicy(applicationConfig app.Config, manager config.Manager, logger kratoslog.Logger, components ComponentsBootstrap) (*app.StopPolicy, func(), error) {
	return app.NewStopPolicy(applicationConfig, manager, logger, components.StopDelay())
}
