package consul

import (
	"context"
	"maps"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
)

type watcher struct {
	// events 传递最新服务快照；新快照会替换尚未读取的旧快照。
	events chan []*registry.ServiceInstance
	// cancel 取消发现查询与重试等待。
	cancel context.CancelFunc
	// done 工作协程退出后关闭，用于等待结束并同步错误读取。
	done chan struct{}
	// err 仅工作协程写入，读取方必须先观察 done 关闭。
	err error
}

func (w *watcher) Next() ([]*registry.ServiceInstance, error) {
	select {
	case <-w.done:
		return nil, w.err
	default:
	}
	select {
	case <-w.done:
		return nil, w.err
	case services := <-w.events:
		return services, nil
	}
}

func (w *watcher) Stop() error {
	w.cancel()
	<-w.done
	return nil
}

func (w *watcher) run(ctx context.Context, d *discovery, name string, indices map[string]uint64) {
	defer close(w.done)
	defer w.cancel()
	backoff := reconnect.Backoff{}
	attempt := 0
	for {
		services, current, err := d.query(ctx, name, indices)
		if ctx.Err() != nil {
			w.err = ctx.Err()
			return
		}
		if err != nil {
			if !retryable(err) {
				w.err = err
				return
			}
			d.logger.With("service", name, "attempt", attempt+1, "error", err).Warn("Consul discovery query failed; retrying")
			if err = backoff.Wait(ctx, attempt); err != nil {
				w.err = err
				return
			}
			attempt++
			continue
		}
		if attempt > 0 {
			d.logger.With("service", name, "attempts", attempt).Info("Consul discovery query recovered")
		}
		attempt = 0
		if !maps.Equal(indices, current) {
			// 只保留最新快照；成功的空列表也必须覆盖旧节点。
			select {
			case <-w.events:
			default:
			}
			select {
			case w.events <- services:
			case <-ctx.Done():
				w.err = ctx.Err()
				return
			}
			indices = current
		}
		// 非阻塞查询或 Consul index=0 时同样避免紧循环。
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			w.err = ctx.Err()
			return
		case <-timer.C:
		}
	}
}
