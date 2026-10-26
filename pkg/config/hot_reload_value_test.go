package config

import (
	"errors"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type hotReloadTestManager struct {
	observer     Observer
	load         func(any) error
	subscribeErr error
	canceled     bool
}

func (m *hotReloadTestManager) Load(_ string, target any, _ ...any) error {
	if m.load != nil {
		return m.load(target)
	}
	*target.(*int) = 1
	return nil
}

func (m *hotReloadTestManager) Subscribe(
	_ string,
	_ any,
	observer Observer,
	_ ...any,
) (func(), error) {
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	m.observer = observer
	return func() { m.canceled = true }, nil
}

func TestHotReloadValuePublishesUpdateOnce(t *testing.T) {
	manager := new(hotReloadTestManager)
	hot, cancel, err := NewHotReloadValue[int](manager, "value")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	done := make(chan struct{})
	next := 2
	go func() {
		manager.observer("value", &next, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not return after publishing update")
	}

	value, version := hot.GetCurrent()
	if *value != 2 || version != 1 {
		t.Fatalf("snapshot = (%d, %d), want (2, 1)", *value, version)
	}
}

// 先加载初值再登记订阅；Manager 回放的当前值以通知形式覆盖初值并推进版本。
func TestHotReloadValueInitialLoadThenReplay(t *testing.T) {
	manager := new(hotReloadTestManager)
	manager.load = func(target any) error {
		if manager.observer != nil {
			t.Fatal("subscription registered before initial load")
		}
		*target.(*int) = 2
		return nil
	}
	hot, cancel, err := NewHotReloadValue[int](manager, "value")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if value, version := hot.GetCurrent(); *value != 2 || version != 0 {
		t.Fatalf("initial snapshot = (%d, %d), want (2, 0)", *value, version)
	}
	// 模拟 Load 与登记之间发生的更新：回放携带最新值。
	next := 3
	manager.observer("value", &next, nil)
	if value, version := hot.GetCurrent(); *value != 3 || version != 1 {
		t.Fatalf("replayed snapshot = (%d, %d), want (3, 1)", *value, version)
	}
}

func TestHotReloadValueInitializationFailure(t *testing.T) {
	failure := errors.New("initialization failed")
	t.Run("load failure skips subscription", func(t *testing.T) {
		manager := &hotReloadTestManager{load: func(any) error { return failure }}
		hot, cancel, err := NewHotReloadValue[int](manager, "value")
		if !errors.Is(err, failure) || hot != nil || cancel != nil || manager.observer != nil {
			t.Fatalf("unexpected result: hot=%v err=%v subscribed=%v", hot, err, manager.observer != nil)
		}
	})
	t.Run("subscribe failure", func(t *testing.T) {
		manager := &hotReloadTestManager{subscribeErr: failure}
		hot, cancel, err := NewHotReloadValue[int](manager, "value")
		if !errors.Is(err, failure) || hot != nil || cancel != nil {
			t.Fatalf("unexpected result: %v, %v", hot, err)
		}
	})
}

func TestHotReloadValueRootRejectionIsIdentifiable(t *testing.T) {
	var event map[string]any
	t.Cleanup(log.SetLogger(pollingLogFunc(func(level kratoslog.Level, fields ...any) error {
		event = map[string]any{"level": level}
		for i := 0; i+1 < len(fields); i += 2 {
			event[fields[i].(string)] = fields[i+1]
		}
		return nil
	})))
	manager := new(hotReloadTestManager)
	hot, cleanup, err := NewHotReloadValue[int](manager, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cause := errors.New("decode failed")
	manager.observer("", nil, cause)
	if value, version := hot.GetCurrent(); *value != 1 || version != 0 {
		t.Fatalf("value=%d version=%d", *value, version)
	}
	if event["level"] != kratoslog.LevelWarn || event["event"] != "config.value.rejected" || event["key"] != "<root>" || event["error"] != cause {
		t.Fatalf("rejection=%v", event)
	}
}
