package app

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

type HookFunc func(context.Context) error
type InitContextHook func(context.Context) context.Context
type InitOptionsHook func([]kratos.Option) []kratos.Option

type OnInitContextHook interface {
	OnInitContext(context.Context) context.Context
}

type OnInitOptionHook interface {
	OnInitOption([]kratos.Option) []kratos.Option
}

type OnBeforeStartHook interface {
	OnBeforeStart(context.Context) error
}

type OnAfterStartHook interface {
	OnAfterStart(context.Context) error
}

type OnBeforeStopHook interface {
	OnBeforeStop(context.Context) error
}

type OnAfterStopHook interface {
	OnAfterStop(context.Context) error
}

type HookManager struct {
	*log.Log
	initCtx     []InitContextHook
	initOptions []InitOptionsHook
	beforeStart []HookFunc
	afterStart  []HookFunc
	beforeStop  []HookFunc
	afterStop   []HookFunc
}

func NewHookManager(log *log.Log) *HookManager {
	return &HookManager{Log: log}
}

func (m *HookManager) Register(hook any) {
	if hook, ok := hook.(OnInitContextHook); ok {
		m.OnInitContext(hook.OnInitContext)
	}

	if hook, ok := hook.(OnInitOptionHook); ok {
		m.OnInitOptions(hook.OnInitOption)
	}

	if hook, ok := hook.(OnBeforeStartHook); ok {
		m.OnBeforeStart(hook.OnBeforeStart)
	}

	if hook, ok := hook.(OnAfterStartHook); ok {
		m.OnAfterStart(hook.OnAfterStart)
	}

	if hook, ok := hook.(OnBeforeStopHook); ok {
		m.OnBeforeStop(hook.OnBeforeStop)
	}

	if hook, ok := hook.(OnAfterStopHook); ok {
		m.OnAfterStop(hook.OnAfterStop)
	}
}

func (m *HookManager) OnInitContext(fn InitContextHook) {
	m.initCtx = append(m.initCtx, fn)
}

func (m *HookManager) OnInitOptions(fn InitOptionsHook) {
	m.initOptions = append(m.initOptions, fn)
}

func (m *HookManager) OnBeforeStart(fn HookFunc) {
	m.beforeStart = append(m.beforeStart, fn)
}

func (m *HookManager) OnAfterStart(fn HookFunc) {
	m.afterStart = append(m.afterStart, fn)
}

func (m *HookManager) OnBeforeStop(fn HookFunc) {
	m.beforeStop = append(m.beforeStop, fn)
}

func (m *HookManager) OnAfterStop(fn HookFunc) {
	m.afterStop = append(m.afterStop, fn)
}
