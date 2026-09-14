package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
)

type demoStep struct {
	name string
	run  func(context.Context, string) error
}
type demoService struct {
	cache   *redis.Client
	steps   []demoStep
	clients client.Factory
	logger  log.Logger
	runs    metric.Int64Counter
}

func newDemoService(data *dataService, storage *objectStorage, messages *messaging, clients client.Factory, logger log.Logger, provider metrics.Provider) (*demoService, error) {
	counter, err := provider.Meter("components.business").Int64Counter("business_demo_runs_total")
	if err != nil {
		return nil, err
	}
	return &demoService{cache: data.cache, steps: []demoStep{{"database-cache-lock", data.Run}, {"oss", storage.Run}, {"kafka-queue", messages.Run}}, clients: clients, logger: logger.WithModule("demo"), runs: counter}, nil
}

func (s *demoService) register(srv server.HTTPServer) {
	routes := []struct {
		method, path, operation string
		handler                 func(context.Context, any) (any, error)
	}{
		{http.MethodPost, "/demo/run", "/example.Components/Run", s.execute},
		{http.MethodGet, "/demo/catalog", "/example.Components/Catalog", s.catalog},
		{http.MethodGet, "/demo/inventory", "/example.Components/Inventory", s.inventory},
	}
	for _, route := range routes {
		srv.Route("/").Handle(route.method, route.path, func(ctx kratoshttp.Context) error {
			// 手写路由必须进入 Kratos 中间件，固定 operation 区分业务且不引入高基数标签。
			kratoshttp.SetOperation(ctx, route.operation)
			result, err := ctx.Middleware(route.handler)(ctx, nil)
			if err != nil {
				return err
			}
			return ctx.Result(http.StatusOK, result)
		})
	}
}

func (s *demoService) catalog(context.Context, any) (any, error) {
	return map[string]any{"products": []string{"foundation-book", "foundation-shirt"}}, nil
}

func (s *demoService) inventory(ctx context.Context, _ any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// 真实读取当前 Redis 库键数量，不将其冒充商品库存；不改变演示数据。
	count, err := s.cache.DBSize(ctx).Result()
	if err != nil {
		s.logger.With("function", "inventory", "error", err).Error("Failed to read inventory from the cache")
		return nil, fmt.Errorf("inventory unavailable")
	}
	return map[string]int64{"cache_keys": count}, nil
}

func (s *demoService) execute(ctx context.Context, _ any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 每轮独立 ID 只用于存储键和日志，不进入指标标签；调用方可串行核对计数增量。
	id := uuid.NewString()
	for _, step := range s.steps {
		if err := step.run(ctx, id); err != nil {
			s.logger.With("function", "execute", "run_id", id, "component", step.name, "error", err).Error("Failed to execute a demonstration component")
			return nil, fmt.Errorf("component demo failed: %s", step.name)
		}
	}
	upstream, _, release, err := s.clients.AcquireClient(ctx, "greeting")
	if err != nil {
		return nil, err
	}
	defer release()
	var reply map[string]string
	if err := upstream.Invoke(ctx, http.MethodGet, "/hello", nil, &reply, kratoshttp.Operation("/example.Greeting/Hello")); err != nil {
		return nil, err
	}
	if reply["message"] != "hello from Foundation" {
		return nil, fmt.Errorf("unexpected greeting response")
	}
	// 通过同一 Factory 出站访问自身两个独立业务路由，形成可对照的 client/server 序列。
	components, _, releaseComponents, err := s.clients.AcquireClient(ctx, "components")
	if err != nil {
		return nil, err
	}
	defer releaseComponents()
	for _, endpoint := range []struct{ path, operation string }{
		{"/demo/catalog", "/example.Components/Catalog"},
		{"/demo/inventory", "/example.Components/Inventory"},
	} {
		var result map[string]any
		if err := components.Invoke(ctx, http.MethodGet, endpoint.path, nil, &result, kratoshttp.Operation(endpoint.operation)); err != nil {
			s.logger.With("function", "execute", "run_id", id, "operation", endpoint.operation, "error", err).Error("Request to a demonstration endpoint failed")
			return nil, err
		}
	}
	s.runs.Add(ctx, 1)
	s.logger.With("function", "execute", "run_id", id).Info("Completed all demonstration requests")
	return map[string]string{"run_id": id, "status": "submitted", "message": "synchronous operations verified; kafka and queue complete asynchronously"}, nil
}
