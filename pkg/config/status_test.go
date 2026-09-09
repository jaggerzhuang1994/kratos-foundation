package config

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestStatusSurvivesBlockedAndOverloadedObserver(t *testing.T) {
	instance, cleanup, err := NewManager(Sources{newManagerSource(`{"feature":{"value":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	reader := instance.(StatusReader)
	initial := reader.Status()
	if !initial.WatcherRunning || initial.Revision != 1 || initial.LastSuccess.IsZero() {
		t.Fatalf("initial=%+v", initial)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	cancel, err := instance.Subscribe("feature", new(managerFeature), func(_ string, value any, err error) {
		if err == nil && value.(*managerFeature).Value == 1 {
			close(blocked)
			<-release
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	publish := func(value int) {
		t.Helper()
		snapshot, err := newValidatedSnapshot(managerJSONValues(fmt.Sprintf(`{"feature":{"value":%d}}`, value)))
		if err != nil {
			t.Fatal(err)
		}
		instance.(*manager).publish(snapshot)
	}
	publish(1)
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}
	for i := 2; i <= 18; i++ {
		publish(i)
	}
	status := reader.Status()
	if status.Overloads != 1 || status.Revision != 19 || !status.WatcherRunning || len(status.Subscriptions) != 1 {
		t.Fatalf("overloaded=%+v", status)
	}
	sub := status.Subscriptions[0]
	if sub.Accepting || !sub.Running || sub.RunningSince.IsZero() || sub.Pending != 17 {
		t.Fatalf("subscription=%+v", sub)
	}
	// 调用方改写状态副本不会污染 Manager；cancel 后累计过载记录仍在。
	status.Subscriptions[0].Key = "modified"
	if reader.Status().Subscriptions[0].Key != "feature" {
		t.Fatal("status aliases manager")
	}
	cancel()
	if after := reader.Status(); len(after.Subscriptions) != 0 || after.Overloads != 1 {
		t.Fatalf("after cancel=%+v", after)
	}
	cleanup()
	if closed := reader.Status(); !closed.Closed || closed.WatcherRunning {
		t.Fatalf("closed=%+v", closed)
	}
}

func TestStatusReportsRejectionRecoveryAndWatcherTermination(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	m, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.close(); err != nil {
			t.Error(err)
		}
	}()
	events := make(chan managerEvent, 4)
	cancel, err := m.Subscribe("feature", new(managerFeature), func(_ string, _ any, err error) { events <- managerEvent{err: err} })
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	receiveManagerEvent(t, events)
	source.update(`{"server":{"middleware":{"timeout":"1s"}}}`)
	if event := receiveManagerEvent(t, events); event.err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	if status := m.Status(); status.RejectedUpdates != 1 || status.Revision != 1 || status.LastErrorCode != "invalid_snapshot" {
		t.Fatalf("rejected=%+v", status)
	}
	source.update(`{"feature":{"value":2}}`)
	receiveManagerEvent(t, events)
	if status := m.Status(); status.AcceptedUpdates != 2 || status.LastErrorCode != "" {
		t.Fatalf("recovered=%+v", status)
	}
	source.fail(errors.New("secret source address"))
	receiveManagerEvent(t, events)
	if status := m.Status(); status.WatcherRunning || status.LastErrorCode != "watcher_stopped" {
		t.Fatalf("stopped=%+v", status)
	}
}
