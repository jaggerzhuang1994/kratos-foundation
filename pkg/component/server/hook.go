package server

import (
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

type Hook struct {
	httpServer []func(*http.Server)
	grpcServer []func(*grpc.Server)
	middleware []Middleware
}

func NewHook(
	middleware DefaultMiddleware,
) *Hook {
	return &Hook{
		middleware: middleware,
	}
}

func (h *Hook) HttpServer(fn func(*http.Server)) {
	h.httpServer = append(h.httpServer, fn)
}

func (h *Hook) GrpcServer(fn func(*grpc.Server)) {
	h.grpcServer = append(h.grpcServer, fn)
}

func (h *Hook) SetMiddleware(m ...Middleware) {
	h.middleware = m
}

func (h *Hook) AddMiddleware(m ...Middleware) {
	h.middleware = append(h.middleware, m...)
}
