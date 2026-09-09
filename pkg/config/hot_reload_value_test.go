package config

import (
	"testing"
	"time"
)

type hotReloadTestManager struct {
	observer Observer
}

func (m *hotReloadTestManager) Load(_ string, target any, _ ...any) error {
	*target.(*int) = 1
	return nil
}

func (m *hotReloadTestManager) Subscribe(
	_ string,
	_ any,
	observer Observer,
	_ ...any,
) (func(), error) {
	m.observer = observer
	return func() {}, nil
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
