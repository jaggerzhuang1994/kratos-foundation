package source

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestOpenRegistersAllWatchersBeforeInitialLoads(t *testing.T) {
	var mu sync.Mutex
	steps := make([]string, 0, 4)
	appendStep := func(step string) {
		mu.Lock()
		steps = append(steps, step)
		mu.Unlock()
	}
	first := newTestSource(&kratosconfig.KeyValue{Key: "first", Value: []byte("old")})
	second := newTestSource(&kratosconfig.KeyValue{Key: "second", Value: []byte("value")})
	first.watchHook = func() {
		appendStep("watch-first")
		first.set(&kratosconfig.KeyValue{Key: "first", Value: []byte("new")})
	}
	second.watchHook = func() { appendStep("watch-second") }
	first.loadHook = func() { appendStep("load-first") }
	second.loadHook = func() { appendStep("load-second") }

	initial, stream, err := Open([]kratosconfig.Source{first, second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	if got := string(initial[0].Value); got != "new" {
		t.Fatalf("initial first value = %q, want new", got)
	}
	wantSteps := []string{"watch-first", "watch-second", "load-first", "load-second"}
	if !reflect.DeepEqual(steps, wantSteps) {
		t.Fatalf("startup steps = %v, want %v", steps, wantSteps)
	}
}

func TestOpenRollsBackWatchersWhenWatchOrLoadFails(t *testing.T) {
	watchErr := errors.New("watch failed")
	loadErr := errors.New("load failed")
	stopErr := errors.New("stop failed")

	t.Run("watch", func(t *testing.T) {
		first := newTestSource()
		first.watcher.stopErr = stopErr
		second := newTestSource()
		second.watchErr = watchErr
		_, stream, err := Open([]kratosconfig.Source{first, second})
		if stream != nil {
			t.Fatal("Open returned watcher after watch failure")
		}
		if !errors.Is(err, watchErr) || !errors.Is(err, stopErr) {
			t.Fatalf("Open error = %v, want watch and stop errors", err)
		}
		if got := first.watcher.stopCalls.Load(); got != 1 {
			t.Fatalf("first Stop calls = %d, want 1", got)
		}
	})

	t.Run("load", func(t *testing.T) {
		first := newTestSource()
		second := newTestSource()
		second.loadErr = loadErr
		_, stream, err := Open([]kratosconfig.Source{first, second})
		if stream != nil {
			t.Fatal("Open returned watcher after load failure")
		}
		if !errors.Is(err, loadErr) {
			t.Fatalf("Open error = %v, want load error", err)
		}
		if first.watcher.stopCalls.Load() != 1 || second.watcher.stopCalls.Load() != 1 {
			t.Fatalf("Stop calls = (%d,%d), want (1,1)", first.watcher.stopCalls.Load(), second.watcher.stopCalls.Load())
		}
	})
}
