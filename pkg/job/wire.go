// Package job 提供 Wire 依赖注入的 ProviderSet
//
// 该文件定义了任务模块的所有依赖注入提供者，通过 Wire 工具
// 自动生成依赖关系的初始化代码。
//
// ProviderSet 包含：
//   - 配置相关：NewConfig, NewDefaultConfig
//   - 日志相关：NewLog, NewCronLog, NewCronLogger
//   - 调度器：NewCron, NewScheduleParser
//   - 中间件：NewMiddleware
//   - 可观测性：otel 提供的 Metrics 和 Tracing
//   - 核心功能：NewRegister, NewBootstrap
//
// 使用方式：
//
//	// 在 wire.go 中导入
//	var ProviderSet = wire.NewSet(
//	    job.ProviderSet,
//	    ...
//	)
package job

import (
	"github.com/google/wire"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/job/otel"
)

// ProviderSet 任务模块的依赖注入提供者集合
//
// 该集合按照依赖顺序组织，确保依赖项在被使用者之前初始化：
//  1. 配置和日志（基础依赖）
//  2. Cron 调度器和解析器
//  3. 中间件和可观测性组件
//  4. 任务注册器和启动引导器（顶层组件）
var ProviderSet = wire.NewSet(
	NewConfig,               // 任务配置
	NewDefaultConfig,        // 默认配置
	NewLog,                  // 任务日志记录器
	NewCronLog,              // Cron 日志配置
	NewCron,                 // Cron 调度器
	NewCronLogger,           // Cron 日志记录器
	NewScheduleParser,       // Cron 表达式解析器
	NewMiddleware,           // 任务中间件链
	otel.NewMetricsProvider, // 指标收集器
	otel.NewTracingProvider, // 链路追踪器
	NewRegister,             // 任务注册器
	NewBootstrap,            // 任务启动引导器
)
