package context

import (
	"context"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/discovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

func NewContext(
	hook Hook,
	appInfo app_info.AppInfo,
	log_ log.Log, // log 组件
	registry_ registry.Registrar, // registry 组件
	discovery_ discovery.Discovery, // discovery 组件
	metrics_ metrics.Metrics, // metric 组件
	tracing_ tracing.Tracing, // tracing 组件
) (context.Context, func()) {
	ctx := context.Background()
	ctx = app_info.NewContext(ctx, appInfo)
	ctx = log.NewContext(ctx, log_)
	ctx = registry.NewContext(ctx, registry_)
	ctx = discovery.NewContext(ctx, discovery_)
	ctx = metrics.NewContext(ctx, metrics_)
	ctx = tracing.NewContext(ctx, tracing_)

	if h, ok := hook.(hookInternal); ok {
		for _, withCtx := range h.getWithContext() {
			ctx = withCtx(ctx)
		}
	}

	ctx, cancel := utils.GracefulShutdownCtx(ctx)
	return ctx, cancel
}
