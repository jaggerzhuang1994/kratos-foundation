package consul

import (
	"context"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

const watchWait = 30 * time.Second

// kvWatcher 的 Next 由调用方顺序调用；Stop 可并发取消请求和退避，不需要后台发送协程。
type kvWatcher struct {
	// source 提供配置前缀的完整快照查询。
	source *kvSource
	// ctx 控制阻塞查询及重试等待的生命周期。
	ctx context.Context
	// cancel 由 Stop 调用以取消查询和等待。
	cancel context.CancelFunc
	// index 保存 Consul 阻塞查询索引，回滚或恢复时重置。
	index uint64
}

func (w *kvWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	logger := log.WithModule("config/consul").WithContext(w.ctx).With("operation", "watch", "path", w.source.path)
	for attempt := 0; ; attempt++ {
		if err := w.ctx.Err(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(w.ctx, watchWait+loadTimeout)
		values, index, err := w.source.query(ctx, w.index, watchWait, watchPhase)
		cancel()
		if err == nil {
			if attempt > 0 {
				logger.With("event", "config.consul.recovered", "attempts", attempt).Info("Consul config recovered")
			}
			// Consul 要求零索引提升到1，避免空前缀查询形成忙循环。
			index = max(index, 1)
			if index < w.index {
				// Consul 重建或回滚后不沿用旧索引，避免阻塞等待一个已不存在的版本。
				logger.With("event", "config.consul.index.reset", "previous_index", w.index,
					"index", index).Warn("Consul config index rolled back; resetting cursor")
				w.index = 0
			} else {
				w.index = index
			}
			return values, nil
		}
		if w.ctx.Err() != nil {
			return nil, w.ctx.Err()
		}
		if !transientConsulError(err) {
			return nil, err
		}
		// 恢复必须重读完整前缀，包含断线期间的删除和空快照。
		w.index = 0
		if attempt == 0 {
			logger.With("event", "config.consul.retrying", "error", err).Warn("Consul config unavailable; retrying")
		}
		if err := (reconnect.Backoff{}).Wait(w.ctx, attempt); err != nil {
			return nil, err
		}
	}
}

func (w *kvWatcher) Stop() error {
	w.cancel()
	return nil
}
