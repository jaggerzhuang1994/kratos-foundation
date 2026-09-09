package source

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

type testResult struct {
	values []*kratosconfig.KeyValue
	err    error
}

type testWatcher struct {
	results   chan testResult
	done      chan struct{}
	stopOnce  sync.Once
	stopCalls atomic.Int32
	stopErr   error
}

func newTestWatcher() *testWatcher {
	return &testWatcher{
		results: make(chan testResult, 32),
		done:    make(chan struct{}),
	}
}

func (w *testWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	select {
	case result := <-w.results:
		return cloneValues(result.values), result.err
	case <-w.done:
		return nil, context.Canceled
	}
}

func (w *testWatcher) Stop() error {
	w.stopCalls.Add(1)
	w.stopOnce.Do(func() { close(w.done) })
	return w.stopErr
}

type testSource struct {
	mu sync.RWMutex

	values    []*kratosconfig.KeyValue
	loadErr   error
	watcher   *testWatcher
	watchErr  error
	watchHook func()
	loadHook  func()
}

func newTestSource(values ...*kratosconfig.KeyValue) *testSource {
	return &testSource{values: cloneValues(values), watcher: newTestWatcher()}
}

func (s *testSource) Load() ([]*kratosconfig.KeyValue, error) {
	if s.loadHook != nil {
		s.loadHook()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return cloneValues(s.values), nil
}

func (s *testSource) Watch() (kratosconfig.Watcher, error) {
	if s.watchHook != nil {
		s.watchHook()
	}
	if s.watchErr != nil {
		return nil, s.watchErr
	}
	return s.watcher, nil
}

func (s *testSource) set(values ...*kratosconfig.KeyValue) {
	s.mu.Lock()
	s.values = cloneValues(values)
	s.mu.Unlock()
}

func receiveUpdate(t testing.TB, stream *Stream) Update {
	t.Helper()
	events := make(chan Update, 1)
	go func() { events <- stream.Next() }()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for source update")
		return Update{}
	}
}

func cloneValues(values []*kratosconfig.KeyValue) []*kratosconfig.KeyValue {
	result := make([]*kratosconfig.KeyValue, 0, len(values))
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
