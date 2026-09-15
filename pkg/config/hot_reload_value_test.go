package config

import (
	"errors"
	"testing"
	"time"
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

func TestHotReloadValueInitializationHandoff(t *testing.T) {
	for _, notify := range []bool{false, true} {
		name := "snapshot changes before initial load"
		if notify {
			name = "callback publishes during initial load"
		}
		t.Run(name, func(t *testing.T) {
			manager := new(hotReloadTestManager)
			manager.load = func(target any) error {
				if manager.observer == nil {
					t.Fatal("initial load ran before subscription")
				}
				*target.(*int) = 2
				if notify {
					// 模拟 Load 已复制旧快照后发生通知；初始化不能覆盖已发布的新值。
					next := 3
					manager.observer("value", &next, nil)
				}
				return nil
			}
			hot, cancel, err := NewHotReloadValue[int](manager, "value")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			want, wantVersion := 2, uint64(0)
			if notify {
				want, wantVersion = 3, 1
			}
			value, version := hot.GetCurrent()
			if *value != want || version != wantVersion {
				t.Fatalf("snapshot = (%d, %d), want (%d, %d)", *value, version, want, wantVersion)
			}
		})
	}
}

func TestHotReloadValueInitializationFailure(t *testing.T) {
	failure := errors.New("initialization failed")
	t.Run("subscribe failure skips load", func(t *testing.T) {
		manager := &hotReloadTestManager{subscribeErr: failure, load: func(any) error { t.Fatal("load ran after failed subscription"); return nil }}
		hot, cancel, err := NewHotReloadValue[int](manager, "value")
		if !errors.Is(err, failure) || hot != nil || cancel != nil {
			t.Fatalf("unexpected result: %v, %v", hot, err)
		}
	})
	t.Run("load failure cancels subscription", func(t *testing.T) {
		manager := &hotReloadTestManager{load: func(any) error { return failure }}
		hot, cancel, err := NewHotReloadValue[int](manager, "value")
		if !errors.Is(err, failure) || hot != nil || cancel != nil || !manager.canceled {
			t.Fatalf("unexpected result: hot=%v err=%v canceled=%v", hot, err, manager.canceled)
		}
	})
}
