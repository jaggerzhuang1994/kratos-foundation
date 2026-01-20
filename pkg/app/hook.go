package app

import (
	"context"
)

// HookFunc 钩子函数
type HookFunc func(context.Context) error

type Hook interface {
	// Register 注册一个hook的实例（可以选择实现 internal_app.OnBeforeStartHook internal_app.OnAfterStartHook internal_app.OnBeforeStopHook internal_app.OnAfterStopHook）
	Register(adapter any)
	// BeforeStart 应用启动前
	BeforeStart(hook HookFunc)
	// AfterStart 应用启动后
	AfterStart(hook HookFunc)
	// BeforeStop 应用停止前
	BeforeStop(hook HookFunc)
	// AfterStop 应用停止后
	AfterStop(hook HookFunc)
}

type hook struct {
	BeforeStartHooks []HookFunc
	AfterStartHooks  []HookFunc
	BeforeStopHooks  []HookFunc
	AfterStopHooks   []HookFunc
}

func NewHook() Hook {
	return &hook{}
}

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

func (m *hook) BeforeStart(hook HookFunc) {
	m.BeforeStartHooks = append(m.BeforeStartHooks, hook)
}

func (m *hook) AfterStart(hook HookFunc) {
	m.AfterStartHooks = append(m.AfterStartHooks, hook)
}

func (m *hook) BeforeStop(hook HookFunc) {
	m.BeforeStopHooks = append(m.BeforeStopHooks, hook)
}

func (m *hook) AfterStop(hook HookFunc) {
	m.AfterStopHooks = append(m.AfterStopHooks, hook)
}

// OnBeforeStartHook 应用启动前钩子
type OnBeforeStartHook interface {
	OnBeforeStart(context.Context) error
}

// OnAfterStartHook 应用启动后钩子
type OnAfterStartHook interface {
	OnAfterStart(context.Context) error
}

// OnBeforeStopHook 应用停止前钩子
type OnBeforeStopHook interface {
	OnBeforeStop(context.Context) error
}

// OnAfterStopHook 应用停止后钩子
type OnAfterStopHook interface {
	OnAfterStop(context.Context) error
}
