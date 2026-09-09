package subscription

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionReplaysBeforeQueuedUpdates(t *testing.T) {
	enteredReplay := make(chan struct{})
	releaseReplay := make(chan struct{})
	events := make(chan int, 2)
	subscription := New(func(notification Notification) {
		value := notification.Value.(int)
		if value == 1 {
			close(enteredReplay)
			<-releaseReplay
		}
		events <- value
	}, errors.New("overloaded"), nil)
	t.Cleanup(subscription.Cancel)

	replayDone := make(chan struct{})
	go func() {
		subscription.Replay(Notification{Value: 1})
		close(replayDone)
	}()
	<-enteredReplay
	if overloaded := subscription.Enqueue(Notification{Value: 2}); overloaded {
		t.Fatal("one queued update overloaded subscription")
	}
	close(releaseReplay)
	<-replayDone
	subscription.Open()

	if first := receiveInt(t, events); first != 1 {
		t.Fatalf("first event = %d, want replay 1", first)
	}
	if second := receiveInt(t, events); second != 2 {
		t.Fatalf("second event = %d, want update 2", second)
	}
}

func TestSubscriptionDeliversUpdatesSequentially(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	done := make(chan struct{}, 2)
	subscription := New(func(Notification) {
		current := active.Add(1)
		for previous := maximum.Load(); current > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		done <- struct{}{}
	}, errors.New("overloaded"), nil)
	subscription.Open()
	t.Cleanup(subscription.Cancel)

	subscription.Enqueue(Notification{Value: 1})
	subscription.Enqueue(Notification{Value: 2})
	<-entered
	select {
	case <-entered:
		t.Fatal("second callback started before first returned")
	default:
	}
	release <- struct{}{}
	<-done
	<-entered
	release <- struct{}{}
	<-done
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent callbacks = %d, want 1", got)
	}
}

func TestSubscriptionKeepsRunningAfterNonTerminalError(t *testing.T) {
	wantErr := errors.New("invalid snapshot")
	events := make(chan Notification, 2)
	subscription := New(func(notification Notification) {
		events <- notification
	}, errors.New("overloaded"), nil)
	subscription.Open()
	t.Cleanup(subscription.Cancel)

	subscription.Enqueue(Notification{Err: wantErr})
	subscription.Enqueue(Notification{Value: "recovered"})
	if event := receiveNotification(t, events); !errors.Is(event.Err, wantErr) || event.Terminal {
		t.Fatalf("error event = %+v", event)
	}
	if event := receiveNotification(t, events); event.Value != "recovered" || event.Err != nil {
		t.Fatalf("recovery event = %+v", event)
	}
}

func TestSubscriptionStopsAfterTerminalError(t *testing.T) {
	wantErr := errors.New("watcher stopped")
	events := make(chan Notification, 2)
	stopped := make(chan struct{})
	subscription := New(func(notification Notification) {
		events <- notification
	}, errors.New("overloaded"), func() {
		close(stopped)
	})
	subscription.Open()

	subscription.Enqueue(Notification{Err: wantErr, Terminal: true})
	subscription.Enqueue(Notification{Value: "late"})
	event := receiveNotification(t, events)
	if !errors.Is(event.Err, wantErr) || !event.Terminal {
		t.Fatalf("terminal event = %+v", event)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("terminal notification did not stop subscription")
	}
	select {
	case late := <-events:
		t.Fatalf("received event after terminal notification: %+v", late)
	default:
	}
}

func TestSubscriptionReportsOverloadAfterAcceptedUpdates(t *testing.T) {
	overloadErr := errors.New("overloaded")
	entered := make(chan struct{})
	release := make(chan struct{})
	events := make(chan Notification, queueCapacity+2)
	var first sync.Once
	subscription := New(func(notification Notification) {
		first.Do(func() {
			close(entered)
			<-release
		})
		events <- notification
	}, overloadErr, nil)
	subscription.Open()

	subscription.Enqueue(Notification{Value: -1})
	<-entered
	for value := 0; value < queueCapacity; value++ {
		if overloaded := subscription.Enqueue(Notification{Value: value}); overloaded {
			t.Fatalf("update %d overloaded before queue was full", value)
		}
	}
	if overloaded := subscription.Enqueue(Notification{Value: queueCapacity}); !overloaded {
		t.Fatal("queue overflow was not reported")
	}
	close(release)

	foundTerminal := false
	for range queueCapacity + 2 {
		event := receiveNotification(t, events)
		if event.Terminal {
			foundTerminal = errors.Is(event.Err, overloadErr)
		}
	}
	if !foundTerminal {
		t.Fatal("overload terminal event was not delivered after accepted updates")
	}
}

func TestSubscriptionCancelDoesNotWaitForRunningCallback(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	subscription := New(func(Notification) {
		close(entered)
		<-release
	}, errors.New("overloaded"), nil)
	subscription.Open()
	subscription.Enqueue(Notification{Value: 1})
	<-entered

	canceled := make(chan struct{})
	go func() {
		subscription.Cancel()
		close(canceled)
	}()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Cancel waited for running callback")
	}
	close(release)
}

func TestSubscriptionIsolatesCallbackPanic(t *testing.T) {
	events := make(chan int, 1)
	subscription := New(func(notification Notification) {
		value := notification.Value.(int)
		if value == 1 {
			panic("boom")
		}
		events <- value
	}, errors.New("overloaded"), nil)
	subscription.Open()
	t.Cleanup(subscription.Cancel)

	subscription.Enqueue(Notification{Value: 1})
	subscription.Enqueue(Notification{Value: 2})
	if got := receiveInt(t, events); got != 2 {
		t.Fatalf("event after panic = %d, want 2", got)
	}
}

func TestSubscriptionCallsOnStopOnce(t *testing.T) {
	var stops atomic.Int32
	subscription := New(func(Notification) {}, errors.New("overloaded"), func() {
		stops.Add(1)
	})
	subscription.Open()
	subscription.Cancel()
	subscription.Cancel()
	if got := stops.Load(); got != 1 {
		t.Fatalf("onStop calls = %d, want 1", got)
	}
}

func receiveInt(t testing.TB, values <-chan int) int {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for integer event")
		return 0
	}
}

func receiveNotification(t testing.TB, values <-chan Notification) Notification {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observer notification")
		return Notification{}
	}
}

func TestQueuePreservesOrderAndCapacity(t *testing.T) {
	t.Parallel()

	var pending queue
	for value := range queueCapacity {
		if pending.full() {
			t.Fatalf("queue full after %d writes, want capacity %d", value, queueCapacity)
		}
		pending.push(Notification{Value: value})
	}
	if !pending.full() {
		t.Fatalf("queue not full after %d writes", queueCapacity)
	}
	for want := range queueCapacity {
		next, ok := pending.pop()
		if !ok || next.Value != want {
			t.Fatalf("pop = (%+v, %t), want value %d", next, ok, want)
		}
	}
	if _, ok := pending.pop(); ok {
		t.Fatal("empty queue returned a notification")
	}
}

func TestStatusRemainsReadableDuringCallbackAndAfterCancellation(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sub := New(func(Notification) { close(entered); <-release; close(finished) }, errors.New("overloaded"), nil)
	defer sub.Cancel()
	defer close(release)
	initial := sub.Status()
	if !initial.Accepting || initial.Pending != 0 || initial.Running {
		t.Fatalf("initial=%+v", initial)
	}
	sub.Open()
	sub.Enqueue(Notification{Value: 1})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback not entered")
	}
	for i := 0; i < queueCapacity; i++ {
		sub.Enqueue(Notification{Value: i})
	}
	if !sub.Enqueue(Notification{}) {
		t.Fatal("expected overload")
	}
	status := sub.Status()
	if status.Accepting || !status.Running || status.RunningSince.IsZero() || status.Pending != queueCapacity+1 {
		t.Fatalf("overloaded=%+v", status)
	}
	sub.Cancel()
	if sub.Status().Accepting {
		t.Fatal("canceled subscription accepts updates")
	}
	// 回调未返回时 cancel 仍应立即完成；由测试释放回调，不遗留协程。
}
