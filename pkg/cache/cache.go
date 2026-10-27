// Package cache 提供基于 Redis 的旁路缓存（cache-aside）：先读缓存，未命中时回源并写回。
// 同一进程内同一 key 的并发回源合并为一次，回源脱离调用方的取消，TTL 带随机抖动，
// 可选缓存“不存在”结果以防缓存穿透。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// ErrNotFound 由回源函数返回，表示数据源确认不存在；配置 NegativeTTL 时该结果也会被缓存。
var ErrNotFound = errors.New("cache: value not found")

// Client 是缓存使用的 Redis 命令子集，*goredis.Client 与 goredis.UniversalClient 均满足。
type Client interface {
	Get(ctx context.Context, key string) *goredis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *goredis.StatusCmd
	Del(ctx context.Context, keys ...string) *goredis.IntCmd
}

// Codec 编解码缓存值，默认 JSON。
type Codec[V any] interface {
	Marshal(V) ([]byte, error)
	Unmarshal([]byte, *V) error
}

// JSONCodec 使用 encoding/json 编解码。
type JSONCodec[V any] struct{}

// Marshal 编码值。
func (JSONCodec[V]) Marshal(value V) ([]byte, error) { return json.Marshal(value) }

// Unmarshal 解码值。
func (JSONCodec[V]) Unmarshal(data []byte, value *V) error { return json.Unmarshal(data, value) }

// Config 在构造时复制，不支持热更新。
type Config struct {
	// KeyPrefix 拼在业务 key 前（中间加冒号），不能为空；不同缓存应使用不同前缀。
	KeyPrefix string
	// TTL 为缓存值的基础过期时间，必须大于 0。
	TTL time.Duration
	// Jitter 为 TTL 的随机浮动比例，实际过期时间落在 TTL×[1-Jitter, 1+Jitter]，避免同批 key 同时过期；
	// 零值默认 0.1，范围 0–0.5，负值表示不加抖动。
	Jitter float64
	// NegativeTTL 大于 0 时缓存回源返回的 ErrNotFound，防止不存在的 key 反复穿透到数据源；零值不缓存。
	NegativeTTL time.Duration
	// LoadTimeout 为单次回源的超时。回源脱离调用方取消，避免第一个调用方取消导致合并等待的其他调用方一起失败；
	// 零值默认 10s。
	LoadTimeout time.Duration
	// Metrics 记录命中、未命中、访问错误与实际回源（business_cache_*）；nil 不记录。
	Metrics *metrics.CacheMetrics
}

// 缓存值首字节区分正常值与“不存在”标记，避免与业务值冲突。
const (
	markerValue    byte = 1
	markerNotFound byte = 0
)

// Cache 是并发安全的旁路缓存；只借用 Redis 连接，不负责关闭。
type Cache[V any] struct {
	client  Client
	codec   Codec[V]
	metrics *metrics.CacheMetrics
	config  Config
	group   singleflight.Group
}

// Option 调整缓存的可选依赖。
type Option[V any] func(*Cache[V])

// WithCodec 替换默认 JSON 编解码。
func WithCodec[V any](codec Codec[V]) Option[V] {
	return func(c *Cache[V]) { c.codec = codec }
}

// New 校验配置，不访问 Redis。
func New[V any](client Client, config Config, options ...Option[V]) (*Cache[V], error) {
	config.KeyPrefix = strings.TrimSpace(config.KeyPrefix)
	switch {
	case client == nil:
		return nil, errors.New("cache requires a redis client")
	case config.KeyPrefix == "":
		return nil, errors.New("cache key prefix is required")
	case config.TTL <= 0:
		return nil, errors.New("cache ttl must be positive")
	case config.Jitter > 0.5:
		return nil, errors.New("cache jitter must not exceed 0.5")
	case config.NegativeTTL < 0 || config.LoadTimeout < 0:
		return nil, errors.New("cache negative ttl and load timeout must not be negative")
	}
	if config.Jitter == 0 {
		config.Jitter = 0.1
	}
	if config.LoadTimeout == 0 {
		config.LoadTimeout = 10 * time.Second
	}
	c := &Cache[V]{client: client, codec: JSONCodec[V]{}, metrics: config.Metrics, config: config}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	if c.codec == nil {
		return nil, errors.New("cache codec must not be nil")
	}
	return c, nil
}

// Key 返回业务 key 对应的 Redis key。
func (c *Cache[V]) Key(key string) string { return c.config.KeyPrefix + ":" + key }

// Get 先读缓存；未命中、值损坏或 Redis 不可用时调用 load 回源并写回。
// 同一 key 的并发回源只执行一次，结果共享给所有等待者；每个调用方仍按自己的 ctx 提前返回。
// Redis 读写失败不影响返回回源结果（降级为直接回源）；load 返回 ErrNotFound 时 Get 也返回 ErrNotFound。
func (c *Cache[V]) Get(ctx context.Context, key string, load func(context.Context) (V, error)) (V, error) {
	var zero V
	redisKey := c.Key(key)
	data, err := c.client.Get(ctx, redisKey).Bytes()
	switch {
	case err == nil:
		value, found, decodeErr := c.decode(data)
		if decodeErr == nil {
			c.hit(ctx)
			if !found {
				return zero, ErrNotFound
			}
			return value, nil
		}
		// 损坏或旧格式的值按未命中处理，回源后覆盖。
		c.miss(ctx)
	case errors.Is(err, goredis.Nil):
		c.miss(ctx)
	default:
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		c.error(ctx)
	}
	result := c.group.DoChan(redisKey, func() (any, error) {
		return c.load(ctx, redisKey, load)
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case shared := <-result:
		if shared.Err != nil {
			return zero, shared.Err
		}
		value, _ := shared.Val.(V) // V 为接口类型且回源返回 nil 时 Val 为 nil。
		return value, nil
	}
}

// load 在脱离调用方取消的 Context 中回源并写回缓存；写回失败不影响结果。
func (c *Cache[V]) load(ctx context.Context, redisKey string, load func(context.Context) (V, error)) (V, error) {
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.config.LoadTimeout)
	defer cancel()
	started := time.Now()
	value, err := load(loadCtx)
	if c.metrics != nil {
		loadErr := err
		if errors.Is(err, ErrNotFound) {
			loadErr = nil
		}
		c.metrics.Load(loadCtx, time.Since(started), loadErr)
	}
	switch {
	case err == nil:
		if encoded, encodeErr := c.codec.Marshal(value); encodeErr == nil {
			_ = c.client.Set(loadCtx, redisKey, append([]byte{markerValue}, encoded...), c.ttl(c.config.TTL)).Err()
		}
		return value, nil
	case errors.Is(err, ErrNotFound) && c.config.NegativeTTL > 0:
		_ = c.client.Set(loadCtx, redisKey, []byte{markerNotFound}, c.ttl(c.config.NegativeTTL)).Err()
	}
	return value, err
}

// Set 主动写入缓存值，例如写库后预热。
func (c *Cache[V]) Set(ctx context.Context, key string, value V) error {
	encoded, err := c.codec.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode cache value: %w", err)
	}
	if err := c.client.Set(ctx, c.Key(key), append([]byte{markerValue}, encoded...), c.ttl(c.config.TTL)).Err(); err != nil {
		return fmt.Errorf("set cache value: %w", err)
	}
	return nil
}

// Delete 删除缓存，写库后调用以失效旧值。
// 删除后、旧值的并发回源仍可能把旧数据写回，强一致场景应在写库提交后延迟再删一次或缩短 TTL。
func (c *Cache[V]) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	redisKeys := make([]string, len(keys))
	for i, key := range keys {
		redisKeys[i] = c.Key(key)
	}
	if err := c.client.Del(ctx, redisKeys...).Err(); err != nil {
		return fmt.Errorf("delete cache value: %w", err)
	}
	return nil
}

func (c *Cache[V]) decode(data []byte) (V, bool, error) {
	var value V
	if len(data) == 0 {
		return value, false, errors.New("empty cache value")
	}
	switch data[0] {
	case markerNotFound:
		if len(data) != 1 {
			return value, false, errors.New("invalid not-found marker")
		}
		return value, false, nil
	case markerValue:
		if err := c.codec.Unmarshal(data[1:], &value); err != nil {
			return value, false, err
		}
		return value, true, nil
	}
	return value, false, errors.New("unknown cache value marker")
}

// ttl 在 [base×(1-Jitter), base×(1+Jitter)] 内随机取值。
func (c *Cache[V]) ttl(base time.Duration) time.Duration {
	if c.config.Jitter <= 0 {
		return base
	}
	spread := time.Duration(float64(base) * c.config.Jitter)
	if spread <= 0 {
		return base
	}
	return base - spread + rand.N(2*spread+1)
}

func (c *Cache[V]) hit(ctx context.Context) {
	if c.metrics != nil {
		c.metrics.Hit(ctx)
	}
}

func (c *Cache[V]) miss(ctx context.Context) {
	if c.metrics != nil {
		c.metrics.Miss(ctx)
	}
}

func (c *Cache[V]) error(ctx context.Context) {
	if c.metrics != nil {
		c.metrics.Error(ctx)
	}
}
