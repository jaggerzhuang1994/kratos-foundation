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

type Hook struct {
	log         *log.Log
	initCtx     []InitContextHook
	initOptions []InitOptionsHook
	beforeStart []HookFunc
	afterStart  []HookFunc
	beforeStop  []HookFunc
	afterStop   []HookFunc
}

func NewHook(log *log.Log) *Hook {
	return &Hook{log: log}
}

func (m *Hook) Register(hook any) {
	if hook, ok := hook.(OnInitContextHook); ok {
		m.InitContext(hook.OnInitContext)
	}

	if hook, ok := hook.(OnInitOptionHook); ok {
		m.InitOptions(hook.OnInitOption)
	}

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

func (m *Hook) InitContext(fn InitContextHook) {
	m.initCtx = append(m.initCtx, fn)
}

func (m *Hook) InitOptions(fn InitOptionsHook) {
	m.initOptions = append(m.initOptions, fn)
}

func (m *Hook) BeforeStart(fn HookFunc) {
	m.beforeStart = append(m.beforeStart, fn)
}

func (m *Hook) AfterStart(fn HookFunc) {
	m.afterStart = append(m.afterStart, fn)
}

func (m *Hook) BeforeStop(fn HookFunc) {
	m.beforeStop = append(m.beforeStop, fn)
}

func (m *Hook) AfterStop(fn HookFunc) {
	m.afterStop = append(m.afterStop, fn)
}
