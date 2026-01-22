package log

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
)

// defaultCallerDepth 默认调用栈深度，用于定位日志调用位置
const defaultCallerDepth = 6

// PresetKv 预设键值对类型，用于存储日志的公共字段
type PresetKv map[string]any

// 预设字段的键名常量
const (
	tsKey            = "ts"            // 时间戳
	serviceIDKey     = "service.id"    // 服务 ID
	serviceNameKey   = "service.name"  // 服务名称
	serviceVersionKey = "service.version" // 服务版本
	traceIDKey       = "trace.id"      // 追踪 ID
	spanIDKey        = "span.id"       // 跨度 ID
	callerKey        = "caller"        // 调用者信息
)

// defaultPreset 默认的预设字段列表
// 注意：callerKey 默认不启用，需要在配置中显式指定
var defaultPreset = []string{
	tsKey,
	serviceIDKey,
	serviceNameKey,
	serviceVersionKey,
	traceIDKey,
	spanIDKey,
	// callerKey, // 默认不启用调用者信息
}

// defaultCaller 默认的调用者信息
var defaultCaller = log.Caller(defaultCallerDepth)

// NewPresetKv 创建预设键值对，用于生成日志的公共字段
// 这些字段包括时间戳、服务信息和追踪信息
//
// 参数：
//   - appInfo: 应用元信息，包含 ID、名称、版本等
//
// 返回：包含所有预设字段的键值对映射
func NewPresetKv(appInfo app_info.AppInfo) PresetKv {
	return PresetKv{
		tsKey: log.DefaultTimestamp, // 默认格式的时间戳
		serviceIDKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetId()
		}),
		serviceNameKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetName()
		}),
		serviceVersionKey: log.Valuer(func(context.Context) interface{} {
			return appInfo.GetVersion()
		}),
		traceIDKey: tracing.TraceID(), // 从 context 中提取追踪 ID
		spanIDKey:  tracing.SpanID(),  // 从 context 中提取跨度 ID
		callerKey:  defaultCaller,     // 默认调用者信息
	}
}

// 下面的代码保留用于未来实现动态值绑定
// var _ = bindValues
//
// bindValues 将键值对中的 Valuer 类型替换为实际值
// func bindValues(ctx context.Context, keyvals []any) {
// 	for i := 1; i < len(keyvals); i += 2 {
// 		if v, ok := keyvals[i].(log.Valuer); ok {
// 			keyvals[i] = v(ctx)
// 		}
// 	}
// }

// containsValuer 检查键值对中是否包含 Valuer 类型
// Valuer 类型需要从 context 中动态获取值
func containsValuer(keyvals []any) bool {
	for i := 1; i < len(keyvals); i += 2 {
		if _, ok := keyvals[i].(log.Valuer); ok {
			return true
		}
	}
	return false
}
