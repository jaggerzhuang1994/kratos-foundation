package config

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// orderedSource 把有序来源组合为官方 Config 的唯一来源：任一来源更新后，
// 按原顺序重新提交全部来源的最近快照，保证后面的来源始终覆盖前面的来源。
//
// 官方 Config 为每个来源各开一个监听循环，并把单个来源的更新合并到当前全部配置之上，
// 低优先级来源的重载会覆盖高优先级值；组合后只剩一个监听循环，重放全部来源也不存在
// 多个循环交错合并旧快照的竞态。
type orderedSource struct {
	// sources 按覆盖顺序排列的预处理来源，停止由 Manager 负责。
	sources []*preprocessedSource
	// mu 保护 latest。
	mu sync.Mutex
	// latest 各来源最近一次成功返回的 KeyValue，下标与 sources 一致。
	latest [][]*KeyValue
	// watcher Load 成功后创建的组合监听器。
	watcher *orderedWatcher
}

func newOrderedSource(sources []*preprocessedSource) *orderedSource {
	return &orderedSource{sources: sources, latest: make([][]*KeyValue, len(sources))}
}

// Load 按顺序逐源 Load 并立即 Watch，与官方逐源 Load、Watch 的顺序一致，缩短加载与监听之间的窗口。
// 任一来源失败时停止已启动的监听器并返回错误。只供官方 Config 调用一次。
func (s *orderedSource) Load() ([]*KeyValue, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &orderedWatcher{source: s, ctx: ctx, cancel: cancel, changed: make(chan struct{}, 1)}
	for index, source := range s.sources {
		values, err := source.Load()
		if err == nil {
			s.mu.Lock()
			s.latest[index] = values
			s.mu.Unlock()
			var watcher Watcher
			if watcher, err = source.Watch(); err == nil {
				w.watchers = append(w.watchers, watcher)
				go w.forward(index, watcher)
				continue
			}
		}
		// 停止错误由各来源缓存，Manager 回滚时经 preprocessedSource.stop 统一返回，这里不重复拼接。
		_ = w.Stop()
		return nil, err
	}
	s.watcher = w
	return s.snapshot(), nil
}

// stop 停止 Load 已启动的组合监听器；官方 merge 在 Load 之后失败时 Config 不会登记它，由 Manager 回滚调用。
func (s *orderedSource) stop() error {
	if s.watcher == nil {
		return nil
	}
	return s.watcher.Stop()
}

// Watch 返回 Load 已启动的组合监听器。
func (s *orderedSource) Watch() (Watcher, error) {
	if s.watcher == nil {
		return nil, errors.New("ordered config source watched before load")
	}
	return s.watcher, nil
}

// snapshot 返回按来源顺序拼接的最近快照；官方 merge 逐个 KeyValue 覆盖，顺序即优先级。
func (s *orderedSource) snapshot() []*KeyValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*KeyValue
	for _, values := range s.latest {
		result = append(result, values...)
	}
	return result
}

// update 记录来源的新快照，内容与上次相同（例如只改 mtime 的文件事件）时返回 false。
func (s *orderedSource) update(index int, values []*KeyValue) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sameValues(s.latest[index], values) {
		return false
	}
	s.latest[index] = values
	return true
}

func sameValues(a, b []*KeyValue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Format != b[i].Format || !bytes.Equal(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

type orderedWatcher struct {
	// source 保存各来源最近快照。
	source *orderedSource
	// watchers 已启动的各来源监听器，由 Stop 停止。
	watchers []Watcher
	// ctx 控制转发任务和 Next 等待的生命周期。
	ctx context.Context
	// cancel 由 Stop 调用。
	cancel context.CancelFunc
	// changed 合并多次更新为一次待处理信号，容量为 1。
	changed chan struct{}
	// stopOnce 保证停止流程只执行一次。
	stopOnce sync.Once
	// stopErr 首次停止的结果。
	stopErr error
}

// retryDelay 是来源监听返回非取消错误后的重试间隔，与官方监听循环一致。
var retryDelay = time.Second

// forward 串行读取单个来源的更新，内容变化时记录快照并发出信号。
// 错误处理沿用官方监听循环：取消即退出，其他错误记录后等待重试。
func (w *orderedWatcher) forward(index int, watcher Watcher) {
	for {
		values, err := watcher.Next()
		if w.ctx.Err() != nil {
			return
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.WithModule("config").With("event", "config.watch.failed", "source", index, "error", err).
				Error("config source watch failed; retrying")
			select {
			case <-w.ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}
		if !w.source.update(index, values) {
			continue
		}
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
}

// Next 等待任一来源变化后返回全部来源按顺序拼接的快照。
func (w *orderedWatcher) Next() ([]*KeyValue, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.changed:
		return w.source.snapshot(), nil
	}
}

// Stop 取消转发并停止全部来源监听器；与官方 Close 一致，不等待转发任务退出。
func (w *orderedWatcher) Stop() error {
	w.stopOnce.Do(func() {
		w.cancel()
		var errs []error
		for _, watcher := range w.watchers {
			if err := watcher.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
		w.stopErr = errors.Join(errs...)
	})
	return w.stopErr
}
