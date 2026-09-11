package main

import (
	"context"
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"go.opentelemetry.io/otel/metric"
)

type greetingService struct{ greetings metric.Int64Counter }

func newGreetingService(provider metrics.Provider) (*greetingService, error) {
	counter, err := provider.Meter("minimal.business").Int64Counter("business_greetings_total")
	if err != nil {
		return nil, err
	}
	return &greetingService{greetings: counter}, nil
}

func (s *greetingService) register(srv server.HTTPServer) {
	srv.Route("/").GET("/hello", func(ctx kratoshttp.Context) error {
		// 普通 HandleFunc 不会自动进入 Kratos 方法中间件；显式调用确保请求指标和追踪生效。
		kratoshttp.SetOperation(ctx, "/example.Greeting/Hello")
		reply, err := ctx.Middleware(s.hello)(ctx, nil)
		if err != nil {
			return err
		}
		return ctx.Result(http.StatusOK, reply)
	})
}

func (s *greetingService) hello(ctx context.Context, _ any) (any, error) {
	s.greetings.Add(ctx, 1)
	return map[string]string{"message": "hello from Foundation"}, nil
}
