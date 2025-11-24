package server

import (
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/server/middleware"
)

type HookManager struct {
	httpServerMiddlewares []func(middleware.Middlewares) middleware.Middlewares
	httpServerOptions     []func(HttpServerOptions) HttpServerOptions
	grpcServerMiddlewares []func(middleware.Middlewares) middleware.Middlewares
	grpcServerOptions     []func(GrpcServerOptions) GrpcServerOptions
}

func NewHookManager() *HookManager {
	return &HookManager{}
}

func (h *HookManager) HttpServerMiddlewares(fn func(middleware.Middlewares) middleware.Middlewares) {
	h.httpServerMiddlewares = append(h.httpServerMiddlewares, fn)
}

func (h *HookManager) HttpServerOptions(fn func(HttpServerOptions) HttpServerOptions) {
	h.httpServerOptions = append(h.httpServerOptions, fn)
}

func (h *HookManager) GrpcServerMiddlewares(fn func(middleware.Middlewares) middleware.Middlewares) {
	h.grpcServerMiddlewares = append(h.grpcServerMiddlewares, fn)
}

func (h *HookManager) GrpcServerOptions(fn func(GrpcServerOptions) GrpcServerOptions) {
	h.grpcServerOptions = append(h.grpcServerOptions, fn)
}
