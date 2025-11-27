package server

import (
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

type HookManager struct {
	serverMiddleware  ServerMiddlewares
	httpServerOptions HttpServerOptions
	grpcServerOptions GrpcServerOptions
	hookGrpcServer    []func(*grpc.Server)
	hookHttpServer    []func(*http.Server)
}

func NewHookManager() *HookManager {
	return &HookManager{}
}

func (h *HookManager) AppendServerMiddleware(middleware ServerMiddlewares) {
	h.serverMiddleware = append(h.serverMiddleware, middleware...)
}

func (h *HookManager) AppendHttpServerOptions(opts HttpServerOptions) {
	h.httpServerOptions = append(h.httpServerOptions, opts...)
}

func (h *HookManager) AppendGrpcServerOptions(opts GrpcServerOptions) {
	h.grpcServerOptions = append(h.grpcServerOptions, opts...)
}

func (h *HookManager) HookGrpcServer(fn func(*grpc.Server)) {
	h.hookGrpcServer = append(h.hookGrpcServer, fn)
}

func (h *HookManager) HookHttpServer(fn func(*http.Server)) {
	h.hookHttpServer = append(h.hookHttpServer, fn)
}
