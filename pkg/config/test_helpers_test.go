package config_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

type observedValue struct {
	value any
	err   error
}

type watcherResult struct {
	values []*config.KeyValue
	err    error
}

type testWatcher struct {
	results   chan watcherResult
	done      chan struct{}
	stopOnce  sync.Once
	stopCalls atomic.Int32
	stopErr   error
}

func newTestWatcher() *testWatcher {
	return &testWatcher{
		results: make(chan watcherResult, 32),
		done:    make(chan struct{}),
	}
}

func (w *testWatcher) Next() ([]*config.KeyValue, error) {
	select {
	case result := <-w.results:
		return cloneKeyValues(result.values), result.err
	case <-w.done:
		return nil, context.Canceled
	}
}

func (w *testWatcher) Stop() error {
	w.stopCalls.Add(1)
	w.stopOnce.Do(func() {
		close(w.done)
	})
	return w.stopErr
}

type testSource struct {
	mu sync.RWMutex

	values    []*config.KeyValue
	loadErr   error
	watcher   *testWatcher
	watchErr  error
	watchHook func(*testSource)
	loaded    chan struct{}

	loadCalls  atomic.Int32
	watchCalls atomic.Int32
}

func newJSONSource(content string) *testSource {
	return &testSource{
		values:  jsonValues(content),
		watcher: newTestWatcher(),
	}
}

func (s *testSource) Load() ([]*config.KeyValue, error) {
	s.loadCalls.Add(1)
	s.mu.RLock()
	loadErr := s.loadErr
	values := cloneKeyValues(s.values)
	loaded := s.loaded
	s.mu.RUnlock()
	if loaded != nil {
		select {
		case loaded <- struct{}{}:
		default:
		}
	}
	if loadErr != nil {
		return nil, loadErr
	}
	return values, nil
}

func (s *testSource) Watch() (kratosconfig.Watcher, error) {
	s.watchCalls.Add(1)
	if s.watchHook != nil {
		s.watchHook(s)
	}
	if s.watchErr != nil {
		return nil, s.watchErr
	}
	if s.watcher == nil {
		return nil, nil
	}
	return s.watcher, nil
}

func (s *testSource) setValues(values []*config.KeyValue) {
	s.mu.Lock()
	s.values = cloneKeyValues(values)
	s.mu.Unlock()
}

func (s *testSource) publish(values, notification []*config.KeyValue) {
	s.setValues(values)
	s.watcher.results <- watcherResult{values: notification}
}

func (s *testSource) publishAndWaitForReload(values []*config.KeyValue) {
	loaded := make(chan struct{}, 1)
	s.mu.Lock()
	s.values = cloneKeyValues(values)
	s.loaded = loaded
	s.mu.Unlock()
	s.watcher.results <- watcherResult{}
	<-loaded
	s.mu.Lock()
	if s.loaded == loaded {
		s.loaded = nil
	}
	s.mu.Unlock()
}

func (s *testSource) failWatcher(err error) {
	s.watcher.results <- watcherResult{err: err}
}

func jsonValues(content string) []*config.KeyValue {
	return []*config.KeyValue{{
		Key:    "config.json",
		Format: config.JSONFormat,
		Value:  []byte(content),
	}}
}

func cloneKeyValues(values []*config.KeyValue) []*config.KeyValue {
	result := make([]*config.KeyValue, 0, len(values))
	for _, value := range values {
		if value == nil {
			result = append(result, nil)
			continue
		}
		next := *value
		next.Value = append([]byte(nil), value.Value...)
		result = append(result, &next)
	}
	return result
}

func receiveObservedValue(t testing.TB, events <-chan observedValue) observedValue {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for config observer event")
		return observedValue{}
	}
}
