package app

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
)

type HookFunc func(context.Context) error
type InitContextHook func(context.Context) context.Context
type InitOptionsHook func([]kratos.Option) []kratos.Option

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
	return &Hook{
		log: log,
	}
}

func (m *Hook) Register(hooker any) {
	if hook, ok := hooker.(OnInitContextHook); ok {
		m.InitContext(hook.OnInitContext)
	}

	if hook, ok := hooker.(OnInitOptionHook); ok {
		m.InitOptions(hook.OnInitOption)
	}

	if hook, ok := hooker.(OnBeforeStartHook); ok {
		m.BeforeStart(hook.OnBeforeStart)
	}

	if hook, ok := hooker.(OnAfterStartHook); ok {
		m.AfterStart(hook.OnAfterStart)
	}

	if hook, ok := hooker.(OnBeforeStopHook); ok {
		m.BeforeStop(hook.OnBeforeStop)
	}

	if hook, ok := hooker.(OnAfterStopHook); ok {
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
