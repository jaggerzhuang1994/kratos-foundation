package cache

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	goredis "github.com/redis/go-redis/v9"
)

// memoryClient 是进程内 Redis 替身，记录写入的 TTL；getErr 非 nil 时模拟 Redis 不可用。
type memoryClient struct {
	mu     sync.Mutex
	values map[string][]byte
	ttls   map[string]time.Duration
	getErr error
}

func newMemoryClient() *memoryClient {
	return &memoryClient{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (m *memoryClient) Get(ctx context.Context, key string) *goredis.StringCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return goredis.NewStringResult("", m.getErr)
	}
	value, ok := m.values[key]
	if !ok {
		return goredis.NewStringResult("", goredis.Nil)
	}
	return goredis.NewStringResult(string(value), nil)
}

func (m *memoryClient) Set(_ context.Context, key string, value any, expiration time.Duration) *goredis.StatusCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = append([]byte(nil), value.([]byte)...)
	m.ttls[key] = expiration
	return goredis.NewStatusResult("OK", nil)
}

func (m *memoryClient) Del(_ context.Context, keys ...string) *goredis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		delete(m.values, key)
	}
	return goredis.NewIntResult(int64(len(keys)), nil)
}

type product struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newTestCache(t *testing.T, client Client, config Config) (*Cache[product], *metrics.CacheMetrics, metrics.Provider) {
	t.Helper()
	provider, cleanup, err := metrics.NewProvider(appinfo.New("cache-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	cacheMetrics, err := metrics.NewCacheMetrics(provider, "products")
	if err != nil {
		t.Fatal(err)
	}
	if config.KeyPrefix == "" {
		config.KeyPrefix = "products"
	}
	if config.TTL == 0 {
		config.TTL = time.Minute
	}
	config.Metrics = cacheMetrics
	c, err := New[product](client, config)
	if err != nil {
		t.Fatal(err)
	}
	return c, cacheMetrics, provider
}

func lookups(t *testing.T, provider metrics.Provider) map[string]float64 {
	t.Helper()
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			name := family.GetName()
			for _, label := range metric.GetLabel() {
				if label.GetName() == "result" {
					name += "/" + label.GetValue()
				}
			}
			if family.GetName() == "business_cache_lookups_total" || family.GetName() == "business_cache_loads_total" {
				result[name] += metric.GetCounter().GetValue()
			}
		}
	}
	return result
}

// 未命中回源并写回，之后命中不再回源；TTL 落在抖动范围内。
func TestCacheAsideLoadsOnceThenHits(t *testing.T) {
	client := newMemoryClient()
	c, _, provider := newTestCache(t, client, Config{TTL: 100 * time.Second})
	var loads atomic.Int32
	load := func(context.Context) (product, error) { loads.Add(1); return product{ID: "p1", Name: "pen"}, nil }
	for range 3 {
		value, err := c.Get(context.Background(), "p1", load)
		if err != nil || value.Name != "pen" {
			t.Fatalf("value=%+v err=%v", value, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loads = %d", loads.Load())
	}
	if ttl := client.ttls["products:p1"]; ttl < 90*time.Second || ttl > 110*time.Second {
		t.Fatalf("ttl %v outside ±10%% jitter", ttl)
	}
	got := lookups(t, provider)
	if got["business_cache_lookups_total/hit"] != 2 || got["business_cache_lookups_total/miss"] != 1 || got["business_cache_loads_total/success"] != 1 {
		t.Fatalf("metrics = %v", got)
	}
}

// 同一 key 的并发未命中只回源一次；第一个调用方取消不影响其他等待者。
func TestCacheAsideCoalescesConcurrentLoads(t *testing.T) {
	c, _, _ := newTestCache(t, newMemoryClient(), Config{})
	release := make(chan struct{})
	started := make(chan struct{})
	var loads atomic.Int32
	load := func(ctx context.Context) (product, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return product{ID: "hot"}, ctx.Err()
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := c.Get(firstCtx, "hot", load)
		firstErr <- err
	}()
	<-started
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			value, err := c.Get(context.Background(), "hot", load)
			if err == nil && value.ID != "hot" {
				err = errors.New("wrong value")
			}
			results <- err
		})
	}
	time.Sleep(50 * time.Millisecond) // 让等待者进入合并。
	cancelFirst()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller err = %v", err)
	}
	close(release)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("waiter failed after first caller canceled: %v", err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
}

// 回源返回 ErrNotFound：配置 NegativeTTL 时缓存“不存在”，否则每次回源。
func TestCacheAsideNegativeCaching(t *testing.T) {
	for _, negative := range []time.Duration{0, 10 * time.Second} {
		client := newMemoryClient()
		c, _, _ := newTestCache(t, client, Config{NegativeTTL: negative, Jitter: -1})
		var loads atomic.Int32
		load := func(context.Context) (product, error) { loads.Add(1); return product{}, ErrNotFound }
		for range 3 {
			if _, err := c.Get(context.Background(), "missing", load); !errors.Is(err, ErrNotFound) {
				t.Fatalf("negative=%v err=%v", negative, err)
			}
		}
		want := int32(3)
		if negative > 0 {
			want = 1
			if client.ttls["products:missing"] != negative {
				t.Fatalf("negative ttl = %v", client.ttls["products:missing"])
			}
		}
		if loads.Load() != want {
			t.Fatalf("negative=%v loads=%d want %d", negative, loads.Load(), want)
		}
	}
}

// Redis 不可用时降级为直接回源并记错误；损坏的缓存值按未命中处理并被覆盖。
func TestCacheAsideDegradesOnRedisErrorsAndCorruption(t *testing.T) {
	client := newMemoryClient()
	c, _, provider := newTestCache(t, client, Config{})
	client.getErr = errors.New("redis down")
	value, err := c.Get(context.Background(), "p1", func(context.Context) (product, error) { return product{ID: "p1"}, nil })
	if err != nil || value.ID != "p1" || lookups(t, provider)["business_cache_lookups_total/error"] != 1 {
		t.Fatalf("degraded get: %+v %v %v", value, err, lookups(t, provider))
	}
	client.getErr = nil
	client.values["products:p2"] = []byte{markerValue, '{'}
	value, err = c.Get(context.Background(), "p2", func(context.Context) (product, error) { return product{ID: "p2"}, nil })
	if err != nil || value.ID != "p2" || string(client.values["products:p2"][1:]) != `{"id":"p2","name":""}` {
		t.Fatalf("corrupt value not replaced: %+v %v %q", value, err, client.values["products:p2"])
	}
	// 业务错误不写缓存，原样返回。
	failure := errors.New("db down")
	if _, err := c.Get(context.Background(), "p3", func(context.Context) (product, error) { return product{}, failure }); !errors.Is(err, failure) {
		t.Fatalf("load error = %v", err)
	}
	if _, ok := client.values["products:p3"]; ok {
		t.Fatal("failed load was cached")
	}
}

func TestCacheSetDeleteAndValidation(t *testing.T) {
	client := newMemoryClient()
	c, _, _ := newTestCache(t, client, Config{})
	if err := c.Set(context.Background(), "p1", product{ID: "p1", Name: "set"}); err != nil {
		t.Fatal(err)
	}
	value, err := c.Get(context.Background(), "p1", func(context.Context) (product, error) { t.Fatal("loaded despite Set"); return product{}, nil })
	if err != nil || value.Name != "set" {
		t.Fatalf("%+v %v", value, err)
	}
	if err := c.Delete(context.Background(), "p1"); err != nil || len(client.values) != 0 || c.Delete(context.Background()) != nil {
		t.Fatalf("delete: %v %v", err, client.values)
	}
	for config, want := range map[Config]string{
		{TTL: time.Second}: "key prefix",
		{KeyPrefix: "p"}:   "ttl must be positive",
		{KeyPrefix: "p", TTL: time.Second, Jitter: 0.6}:     "jitter",
		{KeyPrefix: "p", TTL: time.Second, NegativeTTL: -1}: "must not be negative",
	} {
		if _, err := New[product](client, config); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("config %+v: err=%v want %q", config, err, want)
		}
	}
	if _, err := New[product](nil, Config{KeyPrefix: "p", TTL: time.Second}); err == nil {
		t.Fatal("nil client accepted")
	}
	if _, err := New(client, Config{KeyPrefix: "p", TTL: time.Second}, WithCodec[product](nil)); err == nil {
		t.Fatal("nil codec accepted")
	}
}

// TestExternalCacheRedis 在真实 Redis 上验证标记字节与 JSON 值的往返、TTL 与删除。
func TestExternalCacheRedis(t *testing.T) {
	addr := os.Getenv("FOUNDATION_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FOUNDATION_TEST_REDIS_ADDR for isolated Redis integration tests")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	prefix := "test:cache:" + uuid.NewString()
	c, err := New[product](client, Config{KeyPrefix: prefix, TTL: time.Minute, NegativeTTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), "p1", "missing") })
	var loads atomic.Int32
	for range 2 {
		value, err := c.Get(ctx, "p1", func(context.Context) (product, error) { loads.Add(1); return product{ID: "p1", Name: "笔"}, nil })
		if err != nil || value.Name != "笔" {
			t.Fatalf("%+v %v", value, err)
		}
		if _, err := c.Get(ctx, "missing", func(context.Context) (product, error) { loads.Add(1); return product{}, ErrNotFound }); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("loads = %d, want 2", loads.Load())
	}
	if ttl := client.TTL(ctx, c.Key("p1")).Val(); ttl <= 50*time.Second || ttl > 66*time.Second {
		t.Fatalf("ttl = %v", ttl)
	}
	if err := c.Delete(ctx, "p1"); err != nil || client.Exists(ctx, c.Key("p1")).Val() != 0 {
		t.Fatalf("delete: %v", err)
	}
}
