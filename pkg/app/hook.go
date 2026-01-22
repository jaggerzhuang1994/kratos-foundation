// Package app 提供应用生命周期管理功能，包括应用创建、配置和 Hook 机制。
//
// Hook 机制允许开发者在应用启动和停止的各个阶段插入自定义逻辑，
// 例如预热缓存、检查依赖、优雅关闭资源等。
//
// 使用示例：
//
//	// 方式一：函数式注册
//	hook.BeforeStart(func(ctx context.Context) error {
//	    // 预热缓存
//	    return warmupCache(ctx)
//	})
//
//	// 方式二：接口实现
//	type MyHook struct{}
//
//	func (h *MyHook) OnBeforeStart(ctx context.Context) error {
//	    // 检查依赖
//	    return checkDependencies(ctx)
//	}
//
//	hook.Register(&MyHook{})
package app

import (
	"context"
)

// HookFunc 钩子函数类型，接收上下文并返回可能的错误
//
// 钩子函数如果返回错误，将阻止应用继续启动或停止流程。
// 建议在钩子函数中实现适当的错误处理和重试逻辑。
type HookFunc func(context.Context) error

// Hook 应用生命周期钩子接口
//
// Hook 提供应用在启动和停止各个阶段的自定义处理能力。
// 通过 Hook，开发者可以在应用生命周期的关键点插入自定义逻辑，
// 而无需修改核心应用代码。
//
// 执行顺序：
//   - OnBeforeStart: 应用启动前（依赖检查、缓存预热等）
//   - OnAfterStart: 应用启动后（健康检查注册、通知发送等）
//   - OnBeforeStop: 应用停止前（优雅关闭、资源释放等）
//   - OnAfterStop: 应用停止后（日志归档、清理临时文件等）
//
// 错误处理：
//   - 如果任一钩子函数返回错误，整个流程将中断
//   - BeforeStart 返回错误会阻止应用启动
//   - BeforeStop 返回错误会强制终止应用
type Hook interface {
	// Register 注册一个钩子实例
	//
	// 参数可以是实现了 OnBeforeStartHook、OnAfterStartHook、
	// OnBeforeStopHook、OnAfterStopHook 中任意接口的对象。
	// 一个对象可以实现多个钩子接口，所有匹配的方法都会被注册。
	//
	// 示例：
	//   type MyHook struct{}
	//
	//   func (h *MyHook) OnBeforeStart(ctx context.Context) error {
	//       return checkDependencies(ctx)
	//   }
	//
	//   func (h *MyHook) OnAfterStart(ctx context.Context) error {
	//       return registerHealthCheck(ctx)
	//   }
	//
	//   hook.Register(&MyHook{})
	Register(adapter any)

	// BeforeStart 注册应用启动前的钩子函数
	//
	// 这些函数在应用 HTTP/gRPC 服务器启动之前执行，适合用于：
	//   - 检查外部依赖（数据库、Redis、消息队列等）
	//   - 预热缓存
	//   - 初始化资源
	//   - 验证配置
	BeforeStart(hook HookFunc)

	// AfterStart 注册应用启动后的钩子函数
	//
	// 这些函数在应用服务器启动完成并开始接受请求后执行，适合用于：
	//   - 发送启动通知（邮件、钉钉、Slack 等）
	//   - 注册到服务发现中心
	//   - 启动后台任务
	//   - 更新应用状态
	AfterStart(hook HookFunc)

	// BeforeStop 注册应用停止前的钩子函数
	//
	// 这些函数在应用停止服务器之前执行，适合用于：
	//   - 优雅关闭数据库连接
	//   - 停止接受新请求
	//   - 等待在途请求完成
	//   - 释放系统资源
	//   - 持久化未完成的任务
	BeforeStop(hook HookFunc)

	// AfterStop 注册应用停止后的钩子函数
	//
	// 这些函数在应用完全停止后执行，适合用于：
	//   - 清理临时文件
	//   - 发送停止通知
	//   - 关闭日志文件
	//   - 上传监控数据
	AfterStop(hook HookFunc)
}

// hook Hook 接口的实现，持有各生命周期阶段的钩子函数列表
//
// 钩子函数按照注册顺序（FIFO）依次执行。
// 每个阶段的钩子函数独立管理，互不干扰。
type hook struct {
	beforeStartHooks []HookFunc // 应用启动前执行的钩子列表
	afterStartHooks  []HookFunc // 应用启动后执行的钩子列表
	beforeStopHooks  []HookFunc // 应用停止前执行的钩子列表
	afterStopHooks   []HookFunc // 应用停止后执行的钩子列表
}

// NewHook 创建一个新的应用生命周期钩子实例
//
// 返回的 Hook 实例是线程安全的，可以在应用初始化阶段并发使用。
func NewHook() Hook {
	return &hook{}
}

// Register 注册钩子实例，通过类型断言识别实现了各阶段钩子接口的方法
//
// 支持一个对象实现多个钩子接口。例如，一个对象可以同时实现
// OnBeforeStartHook 和 OnBeforeStopHook，那么它的两个方法都会被注册。
//
// 类型断言按顺序检查所有钩子接口，如果对象实现了某个接口，
// 对应的方法就会被添加到该阶段的钩子列表中。
//
// 注意：
//   - 如果传入的对象没有实现任何钩子接口，此方法不产生任何效果
//   - 同一个对象的同一方法只能被注册一次
//   - 如果需要多次注册，请使用函数式注册方法（BeforeStart 等）
func (m *hook) Register(hook any) {
	if hook, ok := hook.(OnBeforeStartHook); ok {
		m.BeforeStart(hook.OnBeforeStart)
	}
	if hook, ok := hook.(OnAfterStartHook); ok {
		m.AfterStart(hook.OnAfterStart)
	}
	if hook, ok := hook.(OnBeforeStopHook); ok {
		m.BeforeStop(hook.OnBeforeStop)
	}
	if hook, ok := hook.(OnAfterStopHook); ok {
		m.AfterStop(hook.OnAfterStop)
	}
}

// BeforeStart 添加应用启动前执行的钩子函数
//
// 多个钩子函数按照添加顺序依次执行。
// 如果任一函数返回错误，后续的钩子函数将不会执行，应用启动流程将被中断。
func (m *hook) BeforeStart(hook HookFunc) {
	m.beforeStartHooks = append(m.beforeStartHooks, hook)
}

// AfterStart 添加应用启动后执行的钩子函数
//
// 即使某个钩子函数返回错误，后续的钩子函数仍会继续执行。
// 这是设计决策，确保应用已经启动后的清理逻辑能够执行。
func (m *hook) AfterStart(hook HookFunc) {
	m.afterStartHooks = append(m.afterStartHooks, hook)
}

// BeforeStop 添加应用停止前执行的钩子函数
//
// 多个钩子函数按照添加顺序依次执行。
// 建议在此阶段执行关键的清理和释放资源操作。
func (m *hook) BeforeStop(hook HookFunc) {
	m.beforeStopHooks = append(m.beforeStopHooks, hook)
}

// AfterStop 添加应用停止后执行的钩子函数
//
// 应用已经完全停止，此时可以执行最后的清理操作。
// 注意：此时服务器已经关闭，无法处理请求。
func (m *hook) AfterStop(hook HookFunc) {
	m.afterStopHooks = append(m.afterStopHooks, hook)
}

// OnBeforeStartHook 应用启动前钩子接口
//
// 实现此接口的类型可以在应用启动前执行自定义逻辑。
// 典型使用场景：
//   - 检查外部依赖的可用性
//   - 初始化缓存
//   - 验证必要的配置项
//   - 预加载静态数据
//
// 错误处理：
//   如果方法返回错误，应用将不会启动。
//   建议返回明确的错误信息，说明启动失败的原因。
type OnBeforeStartHook interface {
	OnBeforeStart(context.Context) error
}

// OnAfterStartHook 应用启动后钩子接口
//
// 实现此接口的类型可以在应用启动后执行自定义逻辑。
// 典型使用场景：
//   - 发送启动通知
//   - 注册到服务发现中心
//   - 启动后台任务或定时器
//   - 初始化监控指标
//
// 注意：
//   此时应用已经开始接受请求。
//   即使此方法返回错误，应用也不会停止。
type OnAfterStartHook interface {
	OnAfterStart(context.Context) error
}

// OnBeforeStopHook 应用停止前钩子接口
//
// 实现此接口的类型可以在应用停止前执行自定义逻辑。
// 典型使用场景：
//   - 优雅关闭数据库连接
//   - 停止接受新请求
//   - 等待在途请求完成
//   - 释放系统资源
//   - 持久化未完成的任务
//
// 错误处理：
//   如果方法返回错误，应用仍会停止，但会记录错误日志。
//   建议在此方法中实现幂等的清理逻辑。
type OnBeforeStopHook interface {
	OnBeforeStop(context.Context) error
}

// OnAfterStopHook 应用停止后钩子接口
//
// 实现此接口的类型可以在应用停止后执行自定义逻辑。
// 典型使用场景：
//   - 清理临时文件
//   - 发送停止通知
//   - 关闭日志文件
//   - 上传监控数据
//   - 释放最后的资源
//
// 注意：
//   此时应用已经完全停止，服务器已关闭。
//   即使此方法返回错误，也不会影响应用状态。
type OnAfterStopHook interface {
	OnAfterStop(context.Context) error
}
