package config

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testSource struct {
	values   []*KeyValue
	watcher  *testWatcher
	loadErr  error
	watchErr error
}
type testWatcher struct {
	events  chan []*KeyValue
	done    chan struct{}
	once    sync.Once
	stopErr error
}

func (s *testSource) Load() ([]*KeyValue, error) { return s.values, s.loadErr }
func (s *testSource) Watch() (Watcher, error)    { return s.watcher, s.watchErr }
func (w *testWatcher) Next() ([]*KeyValue, error) {
	select {
	case values := <-w.events:
		return values, nil
	case <-w.done:
		return nil, context.Canceled
	}
}
func (w *testWatcher) Stop() error { w.once.Do(func() { close(w.done) }); return w.stopErr }
func newTestSource(text string) *testSource {
	return &testSource{values: jsonValues(text), watcher: &testWatcher{events: make(chan []*KeyValue, 8), done: make(chan struct{})}}
}
func jsonValues(text string) []*KeyValue {
	return []*KeyValue{{Key: "test.json", Format: JSONFormat, Value: []byte(text)}}
}
func receive(t *testing.T, events <-chan int) int {
	t.Helper()
	select {
	case v := <-events:
		return v
	case <-time.After(time.Second):
		t.Fatal("missing callback")
		return 0
	}
}

func TestManagerOfficialMergeAndIndependentObservers(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "10ms")
	source := newTestSource(`{"feature":{"value":1,"keep":true}}`)
	m, cleanup, err := NewManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	first := make(chan int, 8)
	second := make(chan int, 8)
	type feature struct {
		Value int
		Keep  bool
	}
	cancel, err := m.Subscribe("feature", new(feature), func(_ string, v any, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		first <- v.(*feature).Value
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	// 首次回放与源更新由不同任务调度；先确认初值，再验证变更通知。
	if got := receive(t, first); got != 1 {
		t.Fatalf("initial value = %d, want 1", got)
	}
	source.watcher.events <- jsonValues(`{"feature":{"value":2}}`)
	if receive(t, first) != 2 {
		t.Fatal("update missing")
	}
	var got feature
	if err := m.Load("feature", &got); err != nil || !got.Keep {
		t.Fatalf("default merge should retain omitted field: %+v %v", got, err)
	}
	cancelSecond, err := m.Subscribe("feature", new(feature), func(_ string, v any, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		second <- v.(*feature).Value
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	if got := receive(t, second); got != 2 {
		t.Fatalf("second observer initial value = %d, want 2", got)
	}
	source.watcher.events <- jsonValues(`{"feature":{"value":3}}`)
	if receive(t, second) != 3 {
		t.Fatal("replacement observer missing")
	}
	if receive(t, first) != 3 {
		t.Fatal("first observer lost")
	}
	cancelSecond()
	cleanup()
	if err := m.Load("feature", &got); !errors.Is(err, ErrManagerClosed) {
		t.Fatal(err)
	}
}

func TestManagerDefaultsEnvironmentAndValidation(t *testing.T) {
	t.Setenv("FOUNDATION_ENV_LITERAL", "${FOUNDATION_ENV_REFERENCE}")
	t.Setenv("FOUNDATION_ENV_REFERENCE", "resolved")
	m, cleanup, err := NewManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value string
	if err := m.Load("FOUNDATION_ENV_LITERAL", &value); err != nil || value != "resolved" {
		t.Fatalf("%q %v", value, err)
	}
	fallback := "default"
	if err := m.Load("missing", &value, &fallback); err != nil || value != fallback {
		t.Fatal(err, value)
	}
	if err := m.Load("missing", &value); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := m.Load("missing", value); err == nil {
		t.Fatal("non pointer accepted")
	}
	var root map[string]any
	if err := m.Load("", &root); err != nil || root["FOUNDATION_ENV_LITERAL"] != "resolved" {
		t.Fatal(err)
	}
	missingCancel, err := m.Subscribe("missing", new(string), func(string, any, error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer missingCancel()
	cancel, err := m.Subscribe("missing", new(string), func(string, any, error) { t.Error("unexpected replay") }, &fallback)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := m.Subscribe("missing", new(string), nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	if _, err := m.Subscribe("missing", "invalid", func(string, any, error) {}); err == nil {
		t.Fatal("invalid prototype accepted")
	}
	cleanup()
	if _, err := m.Subscribe("missing", new(string), func(string, any, error) {}); !errors.Is(err, ErrManagerClosed) {
		t.Fatal(err)
	}
}

func TestManagerConstructionRollback(t *testing.T) {
	for _, kind := range []string{"load", "watch", "decode"} {
		t.Run(kind, func(t *testing.T) {
			first := newTestSource(`{}`)
			second := newTestSource(`{}`)
			failure := errors.New("failure")
			switch kind {
			case "load":
				second.loadErr = failure
			case "watch":
				second.watchErr = failure
			case "decode":
				second.values = jsonValues(`{`)
			}
			first.watcher.stopErr = errors.New("stop failed")
			if _, _, err := NewManager(Sources{first, second}); err == nil || !errors.Is(err, first.watcher.stopErr) {
				t.Fatal(err)
			}
			select {
			case <-first.watcher.done:
			default:
				t.Fatal("watcher leaked")
			}
		})
	}
	if _, _, err := NewManager(Sources{nil}); err == nil {
		t.Fatal("nil source accepted")
	}
}

func TestManagerTemplateAndOfficialResolver(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "10ms")
	t.Setenv("FOUNDATION_TEMPLATE_VALUE", "8080")
	source := newTestSource(`{"port":${FOUNDATION_TEMPLATE_VALUE},"database":{"host":"localhost"},"dsn":"postgres://$${database.host}:$${database.port:5432}/app","missing":"$${foundation_missing_key}"}`)
	manager, cleanup, err := NewManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var port int
	if err := manager.Load("port", &port); err != nil || port != 8080 {
		t.Fatal(port, err)
	}
	var dsn string
	if err := manager.Load("dsn", &dsn); err != nil || dsn != "postgres://localhost:5432/app" {
		t.Fatal(dsn, err)
	}
	var missing string
	if err := manager.Load("missing", &missing); err != nil || missing != "" {
		t.Fatal(missing, err)
	}
	updates := make(chan string, 1)
	cancel, err := manager.Subscribe("dsn", new(string), func(_ string, value any, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		updates <- *value.(*string)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	// 先消费首次回放，避免将合法初值误判为 resolver 未更新。
	select {
	case got := <-updates:
		if got != "postgres://localhost:5432/app" {
			t.Fatalf("initial dsn = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("initial resolver value not delivered")
	}
	source.watcher.events <- jsonValues(`{"database":{"host":"remote"},"dsn":"postgres://$${database.host}:$${database.port:5432}/app"}`)
	select {
	case got := <-updates:
		if got != "postgres://remote:5432/app" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("resolver update not delivered")
	}
}

func TestNewSourcesFiltersNilWithoutReordering(t *testing.T) {
	first, second := newTestSource(`{}`), newTestSource(`{}`)
	sources := NewSources(nil, first, nil, second)
	if len(sources) != 2 || sources[0] != first || sources[1] != second {
		t.Fatal(sources)
	}
}
