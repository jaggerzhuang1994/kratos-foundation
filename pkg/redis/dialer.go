package redis

import (
	"context"
	"net"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	goredis "github.com/redis/go-redis/v9"
)

// reconnectDialer 只重试建连，不重放 Redis 命令；网络和 TLS 仍由 SDK NewDialer 处理。
func reconnectDialer(options *goredis.Options) func(context.Context, string, string) (net.Conn, error) {
	attempts := options.DialerRetries
	if attempts == 0 {
		attempts = 5
	}
	backoff := reconnect.Backoff{Min: options.DialerRetryTimeout}
	// NewClient 会克隆 Options；提前补齐 NewDialer 捕获的拨号超时。
	if options.DialTimeout == 0 {
		options.DialTimeout = 5 * time.Second
	}
	dial := goredis.NewDialer(options)
	// SDK 不再叠加固定间隔的拨号重试。
	options.DialerRetries = 1
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		for attempt := 0; ; attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, err := dial(ctx, network, address)
			if err == nil {
				return conn, nil
			}
			if attempt+1 >= attempts || !reconnect.Transient(err) {
				return nil, err
			}
			if err := backoff.Wait(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
}
