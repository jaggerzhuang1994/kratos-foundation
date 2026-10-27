package log

import (
	"io"
	"slices"
	"sync"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// 只统计 WARN 及以上的 rejected / panicked / panic.recovered 事件；日志级别关闭时也计数。
func TestEventCounterCountsAlertableEvents(t *testing.T) {
	var mu sync.Mutex
	var counted []string
	restore := SetEventCounter(func(event string) {
		mu.Lock()
		defer mu.Unlock()
		counted = append(counted, event)
	})
	defer restore()
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	// 根级别设为 FATAL：WARN/ERROR 不输出，但仍要计数。
	base := &logger{shared: shared, config: &configState{level: kratoslog.LevelFatal, output: kratoslog.NewStdLogger(io.Discard)}}
	base.With("event", "client.config.rejected").Warn("client config rejected; keeping previous")
	base.With("event", "server.request.panic.recovered").Error("request panic recovered")
	base.Errorw("event", "app.runtime.panicked", "msg", "runtime panic recovered")
	base.Warnf("plain warning %d", 1)
	base.With("event", "server.request.completed").Error("not counted: suffix")
	base.With("event", "client.config.rejected").Info("not counted: level")
	base.Warnw("event", 42, "msg", "not counted: non-string event")
	want := []string{"client.config.rejected", "server.request.panic.recovered", "app.runtime.panicked"}
	if !slices.Equal(counted, want) {
		t.Fatalf("counted = %v, want %v", counted, want)
	}
	restore()
	base.With("event", "client.config.rejected").Warn("after restore")
	if len(counted) != len(want) {
		t.Fatal("counter still active after restore")
	}
	for event, want := range map[string]bool{"job.config.rejected": true, "config.observer.panicked": true, "server.websocket.panic.recovered": true, "app.ready": false, "rejected": false} {
		if CountedEvent(event) != want {
			t.Errorf("CountedEvent(%q) = %v", event, !want)
		}
	}
}
