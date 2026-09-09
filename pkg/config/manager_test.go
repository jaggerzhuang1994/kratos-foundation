package config

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

type managerWatcherResult struct {
	err error
}

type managerWatcher struct {
	results  chan managerWatcherResult
	done     chan struct{}
	stopOnce sync.Once
}

func newManagerWatcher() *managerWatcher {
	return &managerWatcher{
		results: make(chan managerWatcherResult, 32),
		done:    make(chan struct{}),
	}
}

func (w *managerWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	select {
	case result := <-w.results:
		return nil, result.err
	case <-w.done:
		return nil, context.Canceled
	}
}

func (w *managerWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.done) })
	return nil
}

type managerSource struct {
	mu      sync.RWMutex
	values  []*kratosconfig.KeyValue
	loadErr error
	watcher *managerWatcher
}

func newManagerSource(content string) *managerSource {
	return &managerSource{
		values:  managerJSONValues(content),
		watcher: newManagerWatcher(),
	}
}

func (s *managerSource) Load() ([]*kratosconfig.KeyValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return cloneManagerValues(s.values), nil
}

func (s *managerSource) Watch() (kratosconfig.Watcher, error) { return s.watcher, nil }

func (s *managerSource) update(content string) {
	s.mu.Lock()
	s.values = managerJSONValues(content)
	s.mu.Unlock()
	s.watcher.results <- managerWatcherResult{}
}

func (s *managerSource) fail(err error) {
	s.watcher.results <- managerWatcherResult{err: err}
}

type managerEvent struct {
	value int
	err   error
}

type managerFeature struct {
	Value int `json:"value"`
}

func TestManagerPublishesOnlyValidSnapshotsAndRecovers(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.close() })

	events := make(chan managerEvent, 3)
	cancel, err := manager.Subscribe("feature", new(managerFeature), func(_ string, value any, err error) {
		events <- managerEvent{value: value.(*managerFeature).Value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	if event := receiveManagerEvent(t, events); event.err != nil || event.value != 1 {
		t.Fatalf("initial event = %+v", event)
	}

	source.update(`{"feature":`)
	failed := receiveManagerEvent(t, events)
	if failed.err == nil {
		t.Fatal("invalid snapshot did not notify observer")
	}
	var current managerFeature
	if err := manager.Load("feature", &current); err != nil {
		t.Fatal(err)
	}
	if current.Value != 1 {
		t.Fatalf("current value after invalid snapshot = %d, want 1", current.Value)
	}

	source.update(`{"feature":{"value":2}}`)
	recovered := receiveManagerEvent(t, events)
	if recovered.err != nil || recovered.value != 2 {
		t.Fatalf("recovered event = %+v", recovered)
	}
}

func TestManagerBecomesUnhealthyAfterTerminalWatcherError(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.close() })

	events := make(chan managerEvent, 2)
	cancel, err := manager.Subscribe("feature", new(managerFeature), func(_ string, value any, err error) {
		events <- managerEvent{value: value.(*managerFeature).Value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveManagerEvent(t, events)

	cause := errors.New("connection lost")
	source.fail(cause)
	failed := receiveManagerEvent(t, events)
	if !errors.Is(failed.err, ErrWatcherStopped) || !errors.Is(failed.err, cause) {
		t.Fatalf("observer error = %v, want watcher stopped and cause", failed.err)
	}
	if err := manager.Load("feature", new(managerFeature)); !errors.Is(err, ErrWatcherStopped) || !errors.Is(err, cause) {
		t.Fatalf("Load error = %v, want watcher stopped and cause", err)
	}
	if _, err := manager.Subscribe("feature", new(managerFeature), func(string, any, error) {}); !errors.Is(err, ErrWatcherStopped) {
		t.Fatalf("Subscribe error = %v, want watcher stopped", err)
	}
}

func TestManagerPreservesReplayBeforeConcurrentUpdate(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.close() })

	enteredReplay := make(chan struct{})
	releaseReplay := make(chan struct{})
	events := make(chan int, 2)
	subscribed := make(chan error, 1)
	go func() {
		_, subscribeErr := manager.Subscribe("feature", new(managerFeature), func(_ string, value any, err error) {
			if err != nil {
				subscribed <- err
				return
			}
			next := value.(*managerFeature).Value
			if next == 1 {
				close(enteredReplay)
				<-releaseReplay
			}
			events <- next
		})
		subscribed <- subscribeErr
	}()
	<-enteredReplay
	source.update(`{"feature":{"value":2}}`)
	close(releaseReplay)
	if err := <-subscribed; err != nil {
		t.Fatal(err)
	}
	if first := receiveManagerInt(t, events); first != 1 {
		t.Fatalf("first event = %d, want 1", first)
	}
	if second := receiveManagerInt(t, events); second != 2 {
		t.Fatalf("second event = %d, want 2", second)
	}
}

func TestManagerSkipsUnchangedValues(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.close() })

	events := make(chan managerEvent, 3)
	cancel, err := manager.Subscribe("feature", new(managerFeature), func(_ string, value any, err error) {
		events <- managerEvent{value: value.(*managerFeature).Value, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveManagerEvent(t, events)

	source.update(`{"feature":{"value":1}}`)
	source.update(`{"feature":{"value":2}}`)
	updated := receiveManagerEvent(t, events)
	if updated.err != nil || updated.value != 2 {
		t.Fatalf("first update callback = %+v, unchanged snapshot was not skipped", updated)
	}
}

func TestManagerRemovesCanceledSubscriptions(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.close() })

	cancel, err := manager.Subscribe("feature", new(managerFeature), func(string, any, error) {})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	manager.mu.Lock()
	watches := len(manager.watches)
	manager.mu.Unlock()
	if watches != 0 {
		t.Fatalf("watch count after cancel = %d, want 0", watches)
	}
}

func TestManagerCloseRejectsOperationsAndDoesNotWaitForCallback(t *testing.T) {
	source := newManagerSource(`{"feature":{"value":1}}`)
	manager, err := newManager(Sources{source})
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	cancel, err := manager.Subscribe("feature", new(managerFeature), func(string, any, error) {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	source.update(`{"feature":{"value":2}}`)
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- manager.close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for running callback")
	}
	if err := manager.Load("feature", new(managerFeature)); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Load after Close = %v, want ErrManagerClosed", err)
	}
	close(release)
}

func receiveManagerEvent(t testing.TB, events <-chan managerEvent) managerEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager event")
		return managerEvent{}
	}
}

func receiveManagerInt(t testing.TB, events <-chan int) int {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager integer")
		return 0
	}
}

func managerJSONValues(content string) []*kratosconfig.KeyValue {
	return []*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(content)}}
}

func cloneManagerValues(values []*kratosconfig.KeyValue) []*kratosconfig.KeyValue {
	result := make([]*kratosconfig.KeyValue, 0, len(values))
	for _, value := range values {
		next := *value
		next.Value = append([]byte(nil), value.Value...)
		result = append(result, &next)
	}
	return result
}
