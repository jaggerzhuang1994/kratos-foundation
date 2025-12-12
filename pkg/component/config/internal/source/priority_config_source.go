package source

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/config"
	"golang.org/x/sync/errgroup"
)

type priorityConfigSource struct {
	sources []config.Source
	cached  map[config.Source][]*config.KeyValue
	lock    sync.Mutex
}

func NewPriorityConfigSource(sources []config.Source) config.Source {
	return &priorityConfigSource{
		sources,
		map[config.Source][]*config.KeyValue{},
		sync.Mutex{},
	}
}

func (p *priorityConfigSource) Load() ([]*config.KeyValue, error) {
	g, _ := errgroup.WithContext(context.Background())
	for _, source := range p.sources {
		src := source
		g.Go(func() error {
			kvs, err := src.Load()
			if err != nil {
				return err
			}
			p.lock.Lock()
			defer p.lock.Unlock()
			p.cached[src] = kvs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	kvs := make([]*config.KeyValue, 0)
	for _, source := range p.sources {
		kvs = append(kvs, p.cached[source]...)
	}
	return kvs, nil
}

func (p *priorityConfigSource) Watch() (config.Watcher, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{
		ch:       make(chan []*config.KeyValue),
		ctx:      ctx,
		cancel:   cancel,
		watchers: make(map[config.Source]config.Watcher, len(p.sources)),
	}

	for i, source := range p.sources {
		var err error
		w.watchers[source], err = source.Watch()
		if err != nil {
			return nil, err
		}
		go p.watch(i, w.watchers[source], w.ch)
	}

	return w, nil
}

func (p *priorityConfigSource) watch(i int, ww config.Watcher, ch chan []*config.KeyValue) {
	for {
		done := func() bool {
			kvs, err := ww.Next()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return true
				}
				time.Sleep(time.Second)
				return false
			}
			p.lock.Lock()
			defer p.lock.Unlock()
			p.cached[p.sources[i]] = kvs
			for j, source := range p.sources {
				if j > i {
					kvs = append(kvs, p.cached[source]...)
				}
			}
			ch <- kvs
			return false
		}()
		if done {
			return
		}
	}
}

type watcher struct {
	ch       chan []*config.KeyValue
	ctx      context.Context
	cancel   context.CancelFunc
	watchers map[config.Source]config.Watcher
}

func (w *watcher) Next() ([]*config.KeyValue, error) {
	select {
	case kv := <-w.ch:
		return kv, nil
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	}
}

func (w *watcher) Stop() error {
	for _, ww := range w.watchers {
		_ = ww.Stop()
	}
	w.cancel()
	return nil
}
