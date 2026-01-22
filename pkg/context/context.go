package context

import (
	"context"
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/discovery"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
)

// NewContext 创建并初始化应用上下文
// 将各种基础设施组件（日志、注册中心、发现、指标、追踪）注入到 context 中
// 同时应用 context hook 允许自定义扩展
//
// 参数：
//   - hook_: Context 钩子，用于注入自定义 context 数据
//   - appInfo: 应用元信息
//   - log_: 日志组件
//   - registry_: 注册中心组件
//   - discovery_: 服务发现组件
//   - metrics_: 指标采集组件
//   - tracing_: 分布式追踪组件
//
// 返回：
//   - context.Context: 初始化好的上下文
//   - func(): 取消函数，用于优雅停机
//   - error: 错误信息
func NewContext(
	hook_ Hook,
	appInfo app_info.AppInfo,
	log_ log.Log, // log 组件
	registry_ registry.Registrar, // registry 组件
	discovery_ discovery.Discovery, // discovery 组件
	metrics_ metrics.Metrics, // metric 组件
	tracing_ tracing.Tracing, // tracing 组件
) (context.Context, func(), error) {
	ctx := context.Background()

	// 将各组件注入到 context 中
	ctx = app_info.NewContext(ctx, appInfo)
	ctx = log.NewContext(ctx, log_)
	ctx = registry.NewContext(ctx, registry_)
	ctx = discovery.NewContext(ctx, discovery_)
	ctx = metrics.NewContext(ctx, metrics_)
	ctx = tracing.NewContext(ctx, tracing_)

	// 应用 context hook，允许自定义扩展
	if h, ok := hook_.(*hook); ok {
		for _, withCtx := range h.withContext {
			ctx = withCtx(ctx)
		}
	} else {
		return nil, nil, errors.New("context.Hook does not implement hook")
	}

	// 包装为支持优雅停机的 context
	ctx, cancel := utils.GracefulShutdownCtx(ctx)
	return ctx, cancel, nil
}
