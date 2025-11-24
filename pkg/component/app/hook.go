package app

import (
	"context"

	"github.com/go-kratos/kratos/v2"
)

type HookFunc func(context.Context) error

type InitContextHook func(context.Context) context.Context

type InitOptionsHook func([]kratos.Option) []kratos.Option

type Hook struct {
	initCtx     []InitContextHook
	initOptions []InitOptionsHook
	beforeStart []HookFunc
	beforeStop  []HookFunc
	afterStart  []HookFunc
	afterStop   []HookFunc
}

func NewHook() *Hook {
	return &Hook{}
}

func (h *Hook) InitContext(fn InitContextHook) {
	h.initCtx = append(h.initCtx, fn)
}

func (h *Hook) InitOptions(fn InitOptionsHook) {
	h.initOptions = append(h.initOptions, fn)
}

func (h *Hook) BeforeStart(fn HookFunc) {
	h.beforeStart = append(h.beforeStart, fn)
}

func (h *Hook) BeforeStop(fn HookFunc) {
	h.beforeStop = append(h.beforeStop, fn)
}

func (h *Hook) AfterStart(fn HookFunc) {
	h.afterStart = append(h.afterStart, fn)
}

func (h *Hook) AfterStop(fn HookFunc) {
	h.afterStop = append(h.afterStop, fn)
}
