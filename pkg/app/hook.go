package app

import (
	"context"
)

// HookFunc 钩子函数类型，接收上下文并返回可能的错误
type HookFunc func(context.Context) error

// Hook 应用生命周期钩子接口
// 提供应用在启动和停止各个阶段的自定义处理能力
type Hook interface {
	// Register 注册一个钩子实例
	// 参数可以是实现了 OnBeforeStartHook、OnAfterStartHook、OnBeforeStopHook、OnAfterStopHook 中任意接口的对象
	Register(adapter any)
	// BeforeStart 注册应用启动前的钩子函数
	BeforeStart(hook HookFunc)
	// AfterStart 注册应用启动后的钩子函数
	AfterStart(hook HookFunc)
	// BeforeStop 注册应用停止前的钩子函数
	BeforeStop(hook HookFunc)
	// AfterStop 注册应用停止后的钩子函数
	AfterStop(hook HookFunc)
}

// hook Hook 接口的实现，持有各生命周期阶段的钩子函数列表
type hook struct {
	beforeStartHooks []HookFunc // 应用启动前执行的钩子列表
	afterStartHooks  []HookFunc // 应用启动后执行的钩子列表
	beforeStopHooks  []HookFunc // 应用停止前执行的钩子列表
	afterStopHooks   []HookFunc // 应用停止后执行的钩子列表
}

// NewHook 创建一个新的应用生命周期钩子实例
func NewHook() Hook {
	return &hook{}
}

// Register 注册钩子实例，通过类型断言识别实现了各阶段钩子接口的方法
// 支持一个对象实现多个钩子接口
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
func (m *hook) BeforeStart(hook HookFunc) {
	m.beforeStartHooks = append(m.beforeStartHooks, hook)
}

// AfterStart 添加应用启动后执行的钩子函数
func (m *hook) AfterStart(hook HookFunc) {
	m.afterStartHooks = append(m.afterStartHooks, hook)
}

// BeforeStop 添加应用停止前执行的钩子函数
func (m *hook) BeforeStop(hook HookFunc) {
	m.beforeStopHooks = append(m.beforeStopHooks, hook)
}

// AfterStop 添加应用停止后执行的钩子函数
func (m *hook) AfterStop(hook HookFunc) {
	m.afterStopHooks = append(m.afterStopHooks, hook)
}

// OnBeforeStartHook 应用启动前钩子接口
// 实现此接口的类型可以在应用启动前执行自定义逻辑
type OnBeforeStartHook interface {
	OnBeforeStart(context.Context) error
}

// OnAfterStartHook 应用启动后钩子接口
// 实现此接口的类型可以在应用启动后执行自定义逻辑
type OnAfterStartHook interface {
	OnAfterStart(context.Context) error
}

// OnBeforeStopHook 应用停止前钩子接口
// 实现此接口的类型可以在应用停止前执行自定义逻辑（如清理资源）
type OnBeforeStopHook interface {
	OnBeforeStop(context.Context) error
}

// OnAfterStopHook 应用停止后钩子接口
// 实现此接口的类型可以在应用停止后执行自定义逻辑
type OnAfterStopHook interface {
	OnAfterStop(context.Context) error
}
