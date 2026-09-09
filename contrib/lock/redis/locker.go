// Package redis 使用 Redis 实现带所有者校验和过期时间的分布式租约。
package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bsm/redislock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	foundationlock "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	goredis "github.com/redis/go-redis/v9"
)

const minimumTTL = time.Millisecond

var (
	errNilOption            = errors.New("redis lock option is nil")
	errInvalidRetryInterval = errors.New("redis lock retry interval must be positive")
)

type locker struct {
	client        *redislock.Client
	keyPrefix     string
	retryInterval time.Duration
}

// New 创建 Redis Locker；它借用 Manager 持有的 client，不接管连接关闭职责。
func New(
	manager foundationredis.Manager,
	values ...Option,
) (foundationlock.Locker, error) {
	options, err := resolveOptions(values...)
	if err != nil {
		return nil, err
	}

	var client *goredis.Client
	if options.connection == "" {
		client = manager.Default()
	} else {
		client, err = manager.Connection(options.connection)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve redis lock connection %q: %w",
				options.connection,
				err,
			)
		}
	}
	if client == nil {
		return nil, errors.New("redis lock connection is nil")
	}
	return newWithClient(client, options), nil
}

// NewDefault 使用默认连接和默认选项创建 Locker，供 Wire 直接注入。
func NewDefault(manager foundationredis.Manager) (foundationlock.Locker, error) {
	return New(manager)
}

// newWithClient 把已解析选项绑定到借用的 Redis client。
func newWithClient(client redislock.RedisClient, options options) *locker {
	return &locker{
		client:        redislock.New(client),
		keyPrefix:     options.keyPrefix,
		retryInterval: options.retryInterval,
	}
}

// Lock 在尚未取得租约时等待竞争及暂时网络故障，直到成功、永久错误或 ctx 结束。
func (l *locker) Lock(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (foundationlock.Lease, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}
	if err := validateRequest(ctx, key, ttl); err != nil {
		return nil, err
	}
	backoff := reconnect.Backoff{}
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := l.tryLock(ctx, key, ttl)
		if err == nil {
			return lease, nil
		}
		if !errors.Is(err, foundationlock.ErrNotAcquired) {
			if !reconnect.Transient(err) {
				return nil, err
			}
			if err := backoff.Wait(ctx, attempt); err != nil {
				return nil, err
			}
			attempt++
			continue
		}
		attempt = 0

		// 每轮新建 timer 使取消分支可以立即停止它，不遗留长期 ticker。
		timer := time.NewTimer(l.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// TryLock 只尝试获取一次，竞争失败统一返回 foundationlock.ErrNotAcquired。
func (l *locker) TryLock(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (foundationlock.Lease, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}
	if err := validateRequest(ctx, key, ttl); err != nil {
		return nil, err
	}
	return l.tryLock(ctx, key, ttl)
}

// tryLock 把 redislock 的依赖错误转换为本仓库稳定的锁契约。
func (l *locker) tryLock(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (foundationlock.Lease, error) {
	held, err := l.client.Obtain(ctx, l.keyPrefix+key, ttl, nil)
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, foundationlock.ErrNotAcquired
	}
	if err != nil {
		return nil, fmt.Errorf("obtain redis lock: %w", err)
	}
	if held == nil {
		return nil, errors.New("redis lock client returned a nil lease")
	}
	return &lease{key: key, held: held}, nil
}

// validate 校验 Locker 是否完成初始化。
func (l *locker) validate() error {
	if l == nil || l.client == nil {
		return errors.New("redis locker is nil")
	}
	if l.retryInterval <= 0 {
		return errInvalidRetryInterval
	}
	return nil
}

type lease struct {
	key  string
	held *redislock.Lock
}

// Key 返回调用方传入的逻辑键，不暴露 Redis 存储前缀。
func (l *lease) Key() string {
	if l == nil {
		return ""
	}
	return l.key
}

// TTL 返回剩余租约时长；已过期或所有者变化统一返回 ErrNotHeld。
func (l *lease) TTL(ctx context.Context) (time.Duration, error) {
	if l == nil || l.held == nil {
		return 0, foundationlock.ErrNotHeld
	}
	ttl, err := l.held.TTL(ctx)
	if err != nil {
		return 0, fmt.Errorf("read redis lock TTL: %w", err)
	}
	if ttl <= 0 {
		return 0, foundationlock.ErrNotHeld
	}
	return ttl, nil
}

// Refresh 仅在所有者仍匹配时延长租约。
func (l *lease) Refresh(ctx context.Context, ttl time.Duration) error {
	if l == nil || l.held == nil {
		return foundationlock.ErrNotHeld
	}
	if err := validateTTL(ttl); err != nil {
		return err
	}
	err := l.held.Refresh(ctx, ttl, nil)
	if errors.Is(err, redislock.ErrNotObtained) ||
		errors.Is(err, redislock.ErrLockNotHeld) {
		return foundationlock.ErrNotHeld
	}
	if err != nil {
		return fmt.Errorf("refresh redis lock: %w", err)
	}
	return nil
}

// Unlock 仅释放仍属于当前租约令牌的锁。
func (l *lease) Unlock(ctx context.Context) error {
	if l == nil || l.held == nil {
		return foundationlock.ErrNotHeld
	}
	err := l.held.Release(ctx)
	if errors.Is(err, redislock.ErrNotObtained) ||
		errors.Is(err, redislock.ErrLockNotHeld) {
		return foundationlock.ErrNotHeld
	}
	if err != nil {
		return fmt.Errorf("release redis lock: %w", err)
	}
	return nil
}

// validateRequest 校验上下文、逻辑键和毫秒精度租约时长。
func validateRequest(ctx context.Context, key string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return errors.New("lock key is required")
	}
	if trimmedKey != key {
		return errors.New("lock key contains surrounding whitespace")
	}
	return validateTTL(ttl)
}

// validateTTL 拒绝小于一毫秒的时长，因为 redislock 会把 TTL 截断为毫秒整数。
func validateTTL(ttl time.Duration) error {
	if ttl < minimumTTL {
		return fmt.Errorf("lock TTL must be at least %s", minimumTTL)
	}
	return nil
}

const (
	defaultKeyPrefix     = "lock:"
	defaultRetryInterval = 100 * time.Millisecond
)

type options struct {
	connection    string
	keyPrefix     string
	retryInterval time.Duration
}

// defaultOptions 返回每次构造独占的默认选项。
func defaultOptions() options {
	return options{
		keyPrefix:     defaultKeyPrefix,
		retryInterval: defaultRetryInterval,
	}
}

// Option 在构造阶段配置 Redis 锁实现。
type Option func(*options)

// WithConnection 选择 pkg/redis 具名连接；空名称使用默认连接。
func WithConnection(name string) Option {
	return func(options *options) {
		options.connection = strings.TrimSpace(name)
	}
}

// WithKeyPrefix 设置 Redis 键前缀；空前缀表示不划分命名空间。
func WithKeyPrefix(prefix string) Option {
	return func(options *options) {
		options.keyPrefix = prefix
	}
}

// WithRetryInterval 设置 Lock 遇到竞争后的重试间隔。
func WithRetryInterval(interval time.Duration) Option {
	return func(options *options) {
		options.retryInterval = interval
	}
}

// resolveOptions 按调用顺序应用选项，并拒绝 nil 或可能忙循环的间隔。
func resolveOptions(values ...Option) (options, error) {
	resolved := defaultOptions()
	for _, apply := range values {
		if apply == nil {
			return options{}, errNilOption
		}
		apply(&resolved)
	}
	if resolved.retryInterval <= 0 {
		return options{}, errInvalidRetryInterval
	}
	return resolved, nil
}
