package config

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitLoad(t *testing.T, m Manager, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		var got int
		if err := m.Load(key, &got); err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become %d", key, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 低优先级来源更新后，高优先级来源的值仍然生效；高优先级来源更新照常覆盖。
func TestManagerReappliesSourcesInOrderOnUpdate(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "5ms")
	low := newTestSource(`{"a":1,"b":1}`)
	high := newTestSource(`{"a":2}`)
	m, cleanup, err := NewManager(Sources{low, high})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	waitLoad(t, m, "a", 2)
	low.watcher.events <- jsonValues(`{"a":10,"b":10}`)
	waitLoad(t, m, "b", 10)
	waitLoad(t, m, "a", 2)
	high.watcher.events <- jsonValues(`{"a":20}`)
	waitLoad(t, m, "a", 20)
	low.watcher.events <- jsonValues(`{"a":11,"b":11}`)
	waitLoad(t, m, "b", 11)
	waitLoad(t, m, "a", 20)
}

// 内容未变化的更新不唤醒官方监听循环；变化的更新只产生一次合并快照。
func TestOrderedWatcherSkipsUnchangedValues(t *testing.T) {
	first := newTestSource(`{"a":1}`)
	second := newTestSource(`{"b":1}`)
	source := newOrderedSource([]*preprocessedSource{{source: first}, {source: second}})
	if _, err := source.Load(); err != nil {
		t.Fatal(err)
	}
	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Stop() }()
	first.watcher.events <- jsonValues(`{"a":1}`)
	first.watcher.events <- jsonValues(`{"a":2}`)
	values, err := watcher.Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || string(values[0].Value) != `{"a":2}` || string(values[1].Value) != `{"b":1}` {
		t.Fatalf("ordered snapshot = %s", valuesText(values))
	}
	second.watcher.events <- jsonValues(`{"b":1}`)
	next := make(chan error, 1)
	go func() { _, err := watcher.Next(); next <- err }()
	select {
	case err := <-next:
		t.Fatalf("unchanged update woke watcher: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-next; !errors.Is(err, context.Canceled) {
		t.Fatalf("Next after Stop = %v", err)
	}
}

// 来源监听失败后重试；任一来源 Watch 失败时 Load 停止已启动的监听器。
func TestOrderedWatcherRetriesErrorsAndRollsBackWatch(t *testing.T) {
	previous := retryDelay
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = previous })
	failing := &errorOnceWatcher{testWatcher: testWatcher{events: make(chan []*KeyValue, 1), done: make(chan struct{})}}
	source := newOrderedSource([]*preprocessedSource{{source: &watcherSource{values: jsonValues(`{"a":1}`), watcher: failing}}})
	if _, err := source.Load(); err != nil {
		t.Fatal(err)
	}
	watcher, err := source.Watch()
	if err != nil {
		t.Fatal(err)
	}
	failing.events <- jsonValues(`{"a":2}`)
	values, err := watcher.Next()
	if err != nil || len(values) != 1 || string(values[0].Value) != `{"a":2}` {
		t.Fatalf("after retry values=%s err=%v", valuesText(values), err)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}

	started := newTestSource(`{"a":1}`)
	broken := newTestSource(`{"b":1}`)
	broken.watchErr = errors.New("watch failed")
	rollback := newOrderedSource([]*preprocessedSource{{source: started}, {source: broken}})
	if _, err := rollback.Load(); !errors.Is(err, broken.watchErr) {
		t.Fatalf("Load error = %v", err)
	}
	if _, err := rollback.Watch(); err == nil {
		t.Fatal("Watch after failed Load succeeded")
	}
	select {
	case <-started.watcher.done:
	default:
		t.Fatal("started watcher not stopped after Watch failure")
	}
}

type watcherSource struct {
	values  []*KeyValue
	watcher Watcher
}

func (s *watcherSource) Load() ([]*KeyValue, error) { return s.values, nil }
func (s *watcherSource) Watch() (Watcher, error)    { return s.watcher, nil }

// errorOnceWatcher 第一次 Next 返回非取消错误，之后按 testWatcher 行为返回。
type errorOnceWatcher struct {
	testWatcher
	failed bool
}

func (w *errorOnceWatcher) Next() ([]*KeyValue, error) {
	if !w.failed {
		w.failed = true
		return nil, errors.New("temporary")
	}
	return w.testWatcher.Next()
}

func valuesText(values []*KeyValue) string {
	text := ""
	for _, value := range values {
		text += string(value.Value) + ";"
	}
	return text
}
