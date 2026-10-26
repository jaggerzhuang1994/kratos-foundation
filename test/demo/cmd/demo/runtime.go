//go:build demo

package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
)

// statsDaemon 是自定义常驻 Runtime：周期读取 Redis 状态，Stop 关闭 stop 通道后 Start 返回。
type statsDaemon struct {
	shop *shop
	stop chan struct{}
	once sync.Once
}

func (d *statsDaemon) Start(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.stop:
			return nil
		case <-ticker.C:
			_, _ = d.shop.redis.DBSize(ctx).Result()
			_ = d.shop.redis.Incr(ctx, "demo:stats:ticks").Err()
		}
	}
}

func (d *statsDaemon) Stop(context.Context) error {
	d.once.Do(func() { close(d.stop) })
	return nil
}

// loadGenerator 以约 15 rps 访问网关：70% 下单、25% 查询已知订单、5% 查询不存在订单。
type loadGenerator struct {
	shop  *shop
	stop  chan struct{}
	once  sync.Once
	mu    sync.Mutex
	known []string
}

func (g *loadGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-g.stop:
					cancel()
					return
				case <-ticker.C:
					g.request(ctx)
				}
			}
		})
	}
	wg.Wait()
	return nil
}

func (g *loadGenerator) Stop(context.Context) error {
	g.once.Do(func() { close(g.stop) })
	return nil
}

func (g *loadGenerator) request(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	gateway, _, release, err := g.shop.clients.AcquireClient(ctx, "gateway")
	if err != nil {
		return
	}
	defer release()
	var reply map[string]any
	switch n := rand.IntN(100); {
	case n < 70:
		path := fmt.Sprintf("/api/orders?sku=sku-%02d", rand.IntN(20))
		if err := gateway.Invoke(ctx, http.MethodPost, path, nil, &reply, kratoshttp.Operation("/gateway.Orders/Create")); err == nil {
			if id, ok := reply["id"].(string); ok {
				g.remember(id)
			}
		}
	case n < 95:
		_ = gateway.Invoke(ctx, http.MethodGet, "/api/orders/"+g.pick(), nil, &reply, kratoshttp.Operation("/gateway.Orders/Get"))
	default:
		_ = gateway.Invoke(ctx, http.MethodGet, "/api/orders/missing-order", nil, &reply, kratoshttp.Operation("/gateway.Orders/Get"))
	}
}

func (g *loadGenerator) remember(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.known = append(g.known, id)
	if len(g.known) > 200 {
		g.known = g.known[len(g.known)-200:]
	}
}

func (g *loadGenerator) pick() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.known) == 0 {
		return "missing-order"
	}
	return g.known[rand.IntN(len(g.known))]
}
