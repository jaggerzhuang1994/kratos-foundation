package source

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestStreamReloadsTheWholeChangedSource(t *testing.T) {
	source := newTestSource(
		&kratosconfig.KeyValue{Key: "first", Value: []byte("one")},
		&kratosconfig.KeyValue{Key: "second", Value: []byte("two")},
	)
	_, stream, err := Open([]kratosconfig.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	source.set(
		&kratosconfig.KeyValue{Key: "first", Value: []byte("updated")},
		&kratosconfig.KeyValue{Key: "second", Value: []byte("two")},
	)
	source.watcher.results <- testResult{values: []*kratosconfig.KeyValue{{Key: "first", Value: []byte("updated")}}}
	event := receiveUpdate(t, stream)
	if event.Err != nil || event.Terminal {
		t.Fatalf("update event = %+v", event)
	}
	if len(event.Values) != 2 || string(event.Values[0].Value) != "updated" || string(event.Values[1].Value) != "two" {
		t.Fatalf("update values = %#v, want full source", event.Values)
	}
}

func TestStreamPreservesSourcePriorityAfterReload(t *testing.T) {
	first := newTestSource(&kratosconfig.KeyValue{Key: "value", Value: []byte("base")})
	second := newTestSource(&kratosconfig.KeyValue{Key: "value", Value: []byte("override")})
	_, stream, err := Open([]kratosconfig.Source{first, second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	first.set(&kratosconfig.KeyValue{Key: "value", Value: []byte("updated-base")})
	first.watcher.results <- testResult{}
	event := receiveUpdate(t, stream)
	if len(event.Values) != 2 || string(event.Values[0].Value) != "updated-base" || string(event.Values[1].Value) != "override" {
		t.Fatalf("ordered values = %#v", event.Values)
	}
}

func TestStreamReportsReloadFailureAsNonTerminalAndRecovers(t *testing.T) {
	reloadErr := errors.New("temporary load failure")
	source := newTestSource(&kratosconfig.KeyValue{Key: "value", Value: []byte("initial")})
	_, stream, err := Open([]kratosconfig.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	source.mu.Lock()
	source.loadErr = reloadErr
	source.mu.Unlock()
	source.watcher.results <- testResult{}
	failed := receiveUpdate(t, stream)
	if !errors.Is(failed.Err, reloadErr) || failed.Terminal {
		t.Fatalf("reload failure event = %+v", failed)
	}

	source.mu.Lock()
	source.loadErr = nil
	source.values = []*kratosconfig.KeyValue{{Key: "value", Value: []byte("recovered")}}
	source.mu.Unlock()
	source.watcher.results <- testResult{}
	recovered := receiveUpdate(t, stream)
	if recovered.Err != nil || len(recovered.Values) != 1 || string(recovered.Values[0].Value) != "recovered" {
		t.Fatalf("recovery event = %+v", recovered)
	}
}

func TestStreamReportsNextFailureAsTerminal(t *testing.T) {
	watchErr := errors.New("connection lost")
	source := newTestSource()
	_, stream, err := Open([]kratosconfig.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	source.watcher.results <- testResult{err: watchErr}
	event := receiveUpdate(t, stream)
	if !errors.Is(event.Err, watchErr) || !event.Terminal {
		t.Fatalf("watcher failure event = %+v", event)
	}
}

func TestStreamCloseIsIdempotentAndAggregatesErrors(t *testing.T) {
	firstErr := errors.New("first stop")
	secondErr := errors.New("second stop")
	first := newTestSource()
	first.watcher.stopErr = firstErr
	second := newTestSource()
	second.watcher.stopErr = secondErr
	_, stream, err := Open([]kratosconfig.Source{first, second})
	if err != nil {
		t.Fatal(err)
	}

	err = stream.Close()
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Stop error = %v, want both errors", err)
	}
	if err := stream.Close(); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("second Stop error = %v, want stable aggregate", err)
	}
	if first.watcher.stopCalls.Load() != 1 || second.watcher.stopCalls.Load() != 1 {
		t.Fatalf("Stop calls = (%d,%d), want (1,1)", first.watcher.stopCalls.Load(), second.watcher.stopCalls.Load())
	}
}

func TestStreamCloseUnblocksPendingDelivery(t *testing.T) {
	source := newTestSource()
	_, stream, err := Open([]kratosconfig.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	source.watcher.results <- testResult{}

	stopped := make(chan error, 1)
	go func() { stopped <- stream.Close() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not unblock pending event delivery")
	}
}

func TestConcurrentSourceUpdatesNeverRollBackTheFinalSnapshot(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	first := newTestSource(&kratosconfig.KeyValue{Key: "a", Value: []byte("0")})
	second := newTestSource(&kratosconfig.KeyValue{Key: "b", Value: []byte("0")})
	_, stream, err := Open([]kratosconfig.Source{first, second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// 每轮只提交两个变更，并在消费完两条更新后进入下一轮。
	// 无论 worker 怎样调度，最后一个快照都必须包含这一轮的两个值。
	for iteration := 1; iteration <= 300000; iteration++ {
		want := strconv.Itoa(iteration)
		first.set(&kratosconfig.KeyValue{Key: "a", Value: []byte(want)})
		second.set(&kratosconfig.KeyValue{Key: "b", Value: []byte(want)})
		first.watcher.results <- testResult{}
		second.watcher.results <- testResult{}
		initial := stream.Next()
		last := stream.Next()
		if initial.Err != nil || last.Err != nil || len(last.Values) != 2 {
			t.Fatalf("updates = %+v, %+v", initial, last)
		}
		if string(last.Values[0].Value) != want || string(last.Values[1].Value) != want {
			t.Fatalf("round %d: first=(%s,%s), last=(%s,%s), want final=(%s,%s)",
				iteration, initial.Values[0].Value, initial.Values[1].Value,
				last.Values[0].Value, last.Values[1].Value, want, want)
		}
	}
}

type recoveringSource struct {
	*testSource
	loads   atomic.Int32
	failure error
}

func (s *recoveringSource) Load() ([]*kratosconfig.KeyValue, error) {
	if s.loads.Add(1) == 2 {
		return nil, s.failure
	}
	return s.testSource.Load()
}

func TestStreamRetriesTransientReloadWithoutSecondNotification(t *testing.T) {
	for _, failure := range []error{io.EOF, os.ErrNotExist} {
		t.Run(failure.Error(), func(t *testing.T) {
			input := &recoveringSource{failure: failure, testSource: newTestSource(&kratosconfig.KeyValue{Key: "value", Value: []byte("initial")})}
			_, stream, err := Open([]kratosconfig.Source{input})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			input.set(&kratosconfig.KeyValue{Key: "value", Value: []byte("recovered")})
			input.watcher.results <- testResult{}
			failed := receiveUpdate(t, stream)
			if !errors.Is(failed.Err, failure) || failed.Terminal {
				t.Fatalf("failure = %+v", failed)
			}
			recovered := receiveUpdate(t, stream)
			if recovered.Err != nil || len(recovered.Values) != 1 || string(recovered.Values[0].Value) != "recovered" {
				t.Fatalf("recovery without another event = %+v", recovered)
			}

		})
	}
}

func TestStreamReportsUnexpectedChildCancellation(t *testing.T) {
	input := newTestSource()
	_, stream, err := Open([]kratosconfig.Source{input})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	input.watcher.results <- testResult{err: context.Canceled}
	event := receiveUpdate(t, stream)
	if !event.Terminal || !errors.Is(event.Err, context.Canceled) {
		t.Fatalf("unexpected cancellation = %+v", event)
	}
}

type cancelingWatcher struct {
	cancel context.CancelFunc
}

func (w cancelingWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	w.cancel()
	return nil, nil
}

func (cancelingWatcher) Stop() error { return nil }

func TestStreamDoesNotReloadWhenNextCompletesAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &recoveringSource{testSource: newTestSource()}
	stream := &Stream{
		ctx: ctx, sources: []kratosconfig.Source{input},
		cached: make([][]*kratosconfig.KeyValue, 1),
	}
	stream.workers.Add(1)
	stream.watch(0, cancelingWatcher{cancel: cancel})
	if got := input.loads.Load(); got != 0 {
		t.Fatalf("Load calls after cancellation = %d, want 0", got)
	}
}
