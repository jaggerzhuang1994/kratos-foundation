package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestPollInterval(t *testing.T) {
	for _, value := range []string{"", "0", "-1s", "invalid", "10"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CONFIG_POLL_INTERVAL", value)
			if _, _, err := NewManager(nil); err == nil {
				t.Fatal("invalid interval accepted")
			}
		})
	}
	t.Setenv("CONFIG_POLL_INTERVAL", "250ms")
	if interval, err := pollInterval(); err != nil || interval != 250*time.Millisecond {
		t.Fatal(interval, err)
	}
}

func TestPollingMissingKeyAndSnapshotIsolation(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "250ms")
	synctest.Test(t, func(t *testing.T) {
		source := newTestSource(`{}`)
		m, cleanup, err := NewManager(Sources{source})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		hot, cancel, err := NewHotReloadValue(m, "later", &struct{ Value int }{Value: 7})
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		var notifications []int
		stop, err := m.Subscribe("later", new(struct{ Value int }), func(_ string, value any, err error) {
			if err != nil {
				t.Error(err)
				return
			}
			got := value.(*struct{ Value int })
			notifications = append(notifications, got.Value)
			got.Value = 99 // 回调对象修改不能污染 Manager 或其他订阅。
		})
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		synctest.Wait()
		if value, version := hot.GetCurrent(); value.Value != 7 || version != 0 {
			t.Fatal(value, version)
		}
		source.watcher.events <- jsonValues(`{"later":{"value":8}}`)
		synctest.Wait()
		var value struct{ Value int }
		if err := m.Load("later", &value); !errors.Is(err, ErrNotFound) {
			t.Fatal("Load must use the last polling snapshot", err)
		}
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if current, version := hot.GetCurrent(); current.Value != 8 || version != 1 {
			t.Fatal(current, version)
		}
		if err := m.Load("later", &value); err != nil || value.Value != 8 {
			t.Fatal(value, err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if !reflect.DeepEqual(notifications, []int{8}) {
			t.Fatal("unchanged snapshot notified", notifications)
		}
	})
}

func TestPollingCallbacksAreSerialAndCancelable(t *testing.T) {
	t.Setenv("CONFIG_POLL_INTERVAL", "1s")
	synctest.Test(t, func(t *testing.T) {
		source := newTestSource(`{"a":0,"b":0}`)
		m, cleanup, err := NewManager(Sources{source})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		release := make(chan struct{})
		var order []string
		_, err = m.Subscribe("a", new(int), func(string, any, error) {
			order = append(order, "a started")
			<-release
			order = append(order, "a finished")
		})
		if err != nil {
			t.Fatal(err)
		}
		cancel, err := m.Subscribe("b", new(int), func(string, any, error) {
			order = append(order, "b")
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = m.Subscribe("b", new(int), func(string, any, error) {
			order = append(order, "third")
		})
		if err != nil {
			t.Fatal(err)
		}
		// 首次回放沿用同一个轮询任务，慢回调也不能被其他首次通知越过。
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if !reflect.DeepEqual(order, []string{"a started"}) {
			t.Fatal("callbacks overlap", order)
		}
		cancel()
		cancel()
		close(release)
		synctest.Wait()
		if !reflect.DeepEqual(order, []string{"a started", "a finished", "third"}) {
			t.Fatal("canceled callback delivered", order)
		}
		// cleanup 可在回调内部执行，不等待自身；后续订阅不应再交付。
		_, err = m.Subscribe("b", new(int), func(string, any, error) { cleanup() })
		if err != nil {
			t.Fatal(err)
		}
		source.watcher.events <- jsonValues(`{"b":2}`)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		var value int
		if err := m.Load("b", &value); !errors.Is(err, ErrManagerClosed) {
			t.Fatal(err)
		}
	})
}

// scanBackend 隔离无法通过普通 Source 稳定制造的 Scan 错误和字段删除。
type scanBackend struct {
	kratosconfig.Config
	values map[string]any
	err    error
}

func (s *scanBackend) Scan(target any) error {
	if s.err != nil {
		return s.err
	}
	data, err := json.Marshal(s.values)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func TestPollingFailureRecoveryAndPresence(t *testing.T) {
	backend := &scanBackend{values: map[string]any{}}
	m := &manager{backend: backend, snapshot: map[string]any{}}
	var calls int
	var lastErr error
	_, err := m.Subscribe("key", new(int), func(_ string, _ any, err error) {
		calls++
		lastErr = err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Subscribe("", new(map[string]any), func(string, any, error) { panic("observer failure") })
	if err != nil {
		t.Fatal(err)
	}
	backend.err = errors.New("scan failed")
	m.poll()
	if calls != 0 {
		t.Fatal("scan failure notified")
	}
	backend.err = nil
	for _, step := range []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{"null appears", map[string]any{"key": nil}, false},
		{"type error", map[string]any{"key": "invalid"}, true},
		{"recovery", map[string]any{"key": 2}, false},
		{"removed", map[string]any{}, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			before := calls
			backend.values = step.values
			m.poll()
			if calls != before+1 || (lastErr != nil) != step.wantErr {
				t.Fatal(calls, lastErr)
			}
		})
	}
	if !errors.Is(lastErr, ErrNotFound) {
		t.Fatal(lastErr)
	}
	m.closed = true
	backend.values = map[string]any{"key": 3}
	m.poll()
	if calls != 4 {
		t.Fatal("closed manager notified")
	}
}

func TestPollingInitialReplayClosesLoadSubscribeWindow(t *testing.T) {
	backend := &scanBackend{values: map[string]any{"key": 1}}
	m := &manager{backend: backend, snapshot: backend.values}
	var loaded int
	if err := m.Load("key", &loaded); err != nil || loaded != 1 {
		t.Fatal(loaded, err)
	}
	// 模拟业务构造期间轮询已发布新值，登记时的新基线不能吞掉这次更新。
	backend.values = map[string]any{"key": 2}
	m.poll()
	var values []int
	_, err := m.Subscribe("key", new(int), func(_ string, value any, err error) {
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, *value.(*int))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatal("Subscribe synchronously replayed")
	}
	m.poll()
	m.poll()
	if !reflect.DeepEqual(values, []int{2}) {
		t.Fatal(values)
	}
}

func TestPollingInitialReplayErrorsAndCancellation(t *testing.T) {
	for _, step := range []struct {
		name     string
		values   map[string]any
		defaults []any
		want     int
		wantErr  bool
	}{
		{name: "missing", values: map[string]any{}, wantErr: true},
		{name: "missing with default", values: map[string]any{}, defaults: []any{new(int)}, want: 0},
		{name: "null", values: map[string]any{"key": nil}},
		{name: "invalid type", values: map[string]any{"key": "invalid"}, wantErr: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			backend := &scanBackend{values: step.values}
			m := &manager{backend: backend, snapshot: step.values}
			calls := 0
			_, err := m.Subscribe("key", new(int), func(_ string, value any, err error) {
				calls++
				if (err != nil) != step.wantErr || *value.(*int) != step.want {
					t.Fatal(value, err)
				}
			}, step.defaults...)
			if err != nil {
				t.Fatal(err)
			}
			cancel, err := m.Subscribe("key", new(int), func(string, any, error) { t.Fatal("canceled subscription replayed") })
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			m.poll()
			m.poll()
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

// pollingLogFunc 通过已有全局日志入口捕获通知事件。
type pollingLogFunc func(kratoslog.Level, ...any) error

func (f pollingLogFunc) Log(level kratoslog.Level, fields ...any) error {
	return f(level, fields...)
}

func TestPollingHotReloadSubscriptionCaller(t *testing.T) {
	var subscription any
	t.Cleanup(log.SetLogger(pollingLogFunc(func(_ kratoslog.Level, fields ...any) error {
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == "subscription" {
				subscription = fields[i+1]
			}
		}
		return nil
	})))
	backend := &scanBackend{values: map[string]any{"key": 1}}
	m := &manager{backend: backend, snapshot: backend.values}
	_, _, line, _ := runtime.Caller(0)
	_, cancel, err := NewHotReloadValue[int](m, "key")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	m.poll()
	if want := fmt.Sprintf("config/polling_test.go:%d", line+1); subscription != want {
		t.Fatalf("subscription = %v, want %s", subscription, want)
	}
}

func TestPollingSubscriptionUpdateLogs(t *testing.T) {
	events := make(chan map[string]any, 64)
	t.Cleanup(log.SetLogger(pollingLogFunc(func(level kratoslog.Level, fields ...any) error {
		event := make(map[string]any)
		for i := 0; i+1 < len(fields); i += 2 {
			event[fields[i].(string)] = fields[i+1]
		}
		switch event["event"] {
		case "config.snapshot.changed", "config.subscription.replayed", "config.subscription.notified", "config.observer.panicked":
			event["level"] = level
			events <- event
		}
		return nil
	})))
	drain := func() (changed []map[string]any, subscriptions []map[string]any) {
		for len(events) > 0 {
			event := <-events
			if event["event"] == "config.snapshot.changed" {
				changed = append(changed, event)
			} else {
				subscriptions = append(subscriptions, event)
			}
		}
		return changed, subscriptions
	}
	backend := &scanBackend{values: map[string]any{"key": "secret-value"}}
	m := &manager{backend: backend, snapshot: backend.values}
	var wantCaller string
	callback := func(string, any, error) {}
	for _, key := range []string{"key", "key", ""} {
		var prototype any = new(string)
		if key == "" {
			prototype = new(map[string]any)
		}
		_, _, line, _ := runtime.Caller(0)
		_, err := m.Subscribe(key, prototype, callback)
		wantCaller = fmt.Sprintf("config/polling_test.go:%d", line+1)
		if err != nil {
			t.Fatal(err)
		}
	}
	cancel, err := m.Subscribe("key", new(string), func(string, any, error) { t.Fatal("canceled callback") })
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, step := range []struct {
		name        string
		values      map[string]any
		scanErr     error
		initial     bool
		changed     []string
		subscribers int
	}{
		{name: "initial replay", values: backend.values, initial: true, subscribers: 3},
		{name: "unchanged", values: backend.values},
		{name: "changed", values: map[string]any{"key": "new-secret-value"}, changed: []string{"key"}, subscribers: 3},
		{name: "decode failure", values: map[string]any{"key": map[string]any{"nested": true}}, changed: []string{"key"}, subscribers: 3},
		{name: "removed", values: map[string]any{}, changed: []string{"key"}, subscribers: 3},
		{name: "scan failure", scanErr: errors.New("scan failed")},
	} {
		t.Run(step.name, func(t *testing.T) {
			backend.values, backend.err = step.values, step.scanErr
			m.poll()
			changed, subscriptions := drain()
			// 同一轮变化只输出一条 INFO，逐订阅通知为 DEBUG。
			if step.changed == nil && len(changed) != 0 || step.changed != nil && (len(changed) != 1 || changed[0]["level"] != kratoslog.LevelInfo ||
				!reflect.DeepEqual(changed[0]["changed"], step.changed) || changed[0]["module"] != "config") {
				t.Fatalf("snapshot change events = %v, want paths %v", changed, step.changed)
			}
			if len(subscriptions) != step.subscribers {
				t.Fatalf("got %d subscription events, want %d", len(subscriptions), step.subscribers)
			}
			for i, event := range subscriptions {
				key := "key"
				if i == 2 {
					key = ""
				}
				_, found := lookup(step.values, key)
				if key == "" {
					key = "<root>"
				}
				wantEvent := "config.subscription.notified"
				if step.initial {
					wantEvent = "config.subscription.replayed"
				}
				if event["event"] != wantEvent || event["level"] != kratoslog.LevelDebug || event["module"] != "config" ||
					event["key"] != key || event["subscription"] != wantCaller {
					t.Fatalf("unexpected event: %v", event)
				}
				if got, present := event["found"]; present != !found || (!found && got != false) {
					t.Fatalf("unexpected presence flag: %v", event)
				}
			}
			for _, event := range append(changed, subscriptions...) {
				for _, value := range event {
					if reflect.DeepEqual(value, "secret-value") || reflect.DeepEqual(value, "new-secret-value") {
						t.Fatal("configuration value leaked into log")
					}
				}
			}
		})
	}
	// 大量字段变更才输出截断标志，避免精简日志丢失列表不完整的提示。
	backend.err = nil
	backend.values = map[string]any{"key": map[string]any{}}
	m.poll()
	drain()
	fields := make(map[string]any)
	for i := 0; i < 33; i++ {
		fields[fmt.Sprintf("field%02d", i)] = i
	}
	backend.values = map[string]any{"key": fields}
	m.poll()
	changed, _ := drain()
	if len(changed) != 1 || changed[0]["changed_truncated"] != true || len(changed[0]["changed"].([]string)) != 32 || changed[0]["changed"].([]string)[0] != "key.field00" {
		t.Fatalf("missing truncation indication: %v", changed)
	}

	// 通过 Manager 接口登记，异常日志也必须指向登记位置而非回调定义。
	panicCallback := func(string, any, error) { panic("callback failed") }
	var configManager Manager = m
	_, _, line, _ := runtime.Caller(0)
	_, err = configManager.Subscribe("key", new(map[string]any), panicCallback)
	if err != nil {
		t.Fatal(err)
	}
	m.poll()
	_, subscriptions := drain()
	if len(subscriptions) != 2 {
		t.Fatalf("got %d events, want replay and panic", len(subscriptions))
	}
	for index, level := range []kratoslog.Level{kratoslog.LevelDebug, kratoslog.LevelError} {
		event := subscriptions[index]
		if event["subscription"] != fmt.Sprintf("config/polling_test.go:%d", line+1) || event["level"] != level {
			t.Fatalf("unexpected subscription caller: %v", event)
		}
	}
}

func TestPollingScanFailureLogsWarningAndRetainsSnapshot(t *testing.T) {
	var event map[string]any
	t.Cleanup(log.SetLogger(pollingLogFunc(func(level kratoslog.Level, fields ...any) error {
		event = map[string]any{"level": level}
		for i := 0; i+1 < len(fields); i += 2 {
			event[fields[i].(string)] = fields[i+1]
		}
		return nil
	})))
	cause := errors.New("scan failed")
	m := &manager{backend: &scanBackend{err: cause}, snapshot: map[string]any{"key": 1}}
	m.poll()
	if m.snapshot["key"] != 1 || event["event"] != "config.snapshot.rejected" || event["level"] != kratoslog.LevelWarn || event["error"] != cause {
		t.Fatalf("snapshot=%v event=%v", m.snapshot, event)
	}
}

type panickingBackend struct{ kratosconfig.Config }

func (panickingBackend) Scan(any) error { panic("source bug") }

// 轮询在后台协程中执行，自定义配置源 panic 时保留上一份快照，不终止进程。
func TestPollRecoversBackendPanic(t *testing.T) {
	previous := map[string]any{"key": 1}
	m := &manager{backend: panickingBackend{}, snapshot: previous}
	m.poll()
	if m.snapshot["key"] != 1 {
		t.Fatalf("snapshot = %v, want previous snapshot", m.snapshot)
	}
}
