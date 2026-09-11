package consul

import (
	"context"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
)

const watchWait = 30 * time.Second

// kvWatcher 的 Next 由调用方顺序调用；Stop 可并发取消请求和退避，不需要后台发送协程。
type kvWatcher struct {
	source *kvSource
	ctx    context.Context
	cancel context.CancelFunc
	index  uint64
}

// FullSnapshot 声明每次成功的 Next 返回本源完整状态；空结果表示全部删除。
// 返回值至少在下一次 Next 前保持稳定，Stream 会复制后发布，不再重复 Load。
func (w *kvWatcher) FullSnapshot() bool { return true }

func (w *kvWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	for attempt := 0; ; attempt++ {
		if err := w.ctx.Err(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(w.ctx, watchWait+loadTimeout)
		values, index, err := w.source.query(ctx, w.index, watchWait)
		cancel()
		if err == nil {
			// Consul 要求零索引提升到1，避免空前缀查询形成忙循环。
			index = max(index, 1)
			if index < w.index {
				// Consul 重建或回滚后不沿用旧索引，避免阻塞等待一个已不存在的版本。
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
		if err := (reconnect.Backoff{}).Wait(w.ctx, attempt); err != nil {
			return nil, err
		}
	}
}

func (w *kvWatcher) Stop() error {
	w.cancel()
	return nil
}
