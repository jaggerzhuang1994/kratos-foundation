package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConsumerRuntimeStopCancelsConsumeAndWaitsForExit(t *testing.T) {
	raw := newBlockingConsumer()
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	startDone := make(chan error, 1)
	go func() { startDone <- runtime.Start(context.Background()) }()
	raw.waitStarted(t)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := <-startDone; err != nil {
		t.Fatalf("Start returned stop cancellation: %v", err)
	}
}

func TestConsumerRuntimeStopPreservesJoinedNonCancellationError(t *testing.T) {
	heartbeatErr := new(heartbeatSentinelError)
	raw := &joinedCancellationConsumer{
		started: make(chan struct{}),
		err:     heartbeatErr,
	}
	observability := newMetricTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	runtime := newTestConsumerRuntimeWithObservability(
		t,
		raw,
		nil,
		observability,
		func(context.Context, *Message) error { return nil },
	)
	startDone := make(chan error, 1)
	go func() { startDone <- runtime.Start(context.Background()) }()
	select {
	case <-raw.started:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := <-startDone
	var gotHeartbeatErr *heartbeatSentinelError
	if !errors.Is(err, heartbeatErr) || !errors.As(err, &gotHeartbeatErr) {
		t.Fatalf("Start error = %v, want heartbeat sentinel", err)
	}
	if sample := findQueueMetricSample(
		t,
		observability.Metrics,
		"queue_consumer_runtime_failures_total",
		map[string]string{"queue_result": "error"},
	); sample.counter != 1 {
		t.Fatalf("runtime failure count = %v, want 1", sample.counter)
	}
	if written := logs.String(); !strings.Contains(written, "queue.consumer.failed") {
		t.Fatalf("runtime failure log = %q", written)
	}
}

func TestConsumerRuntimePanicDefersCoordinatorCleanup(t *testing.T) {
	raw := &panickingConsumer{captured: make(chan context.Context, 1)}
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Stop(stopCtx)
	})
	recovered := make(chan any, 1)
	go func() {
		defer func() { recovered <- recover() }()
		_ = runtime.Start(context.Background())
	}()
	runCtx := <-raw.captured
	if value := <-recovered; value != "raw consumer panic" {
		t.Fatalf("recovered = %#v, want raw consumer panic", value)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Consumer Context remained live after panic")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Stop(stopCtx); err != nil {
		t.Fatalf("Stop after panic = %v", err)
	}
}

func TestConsumerRuntimeRejectsSecondStart(t *testing.T) {
	raw := newBlockingConsumer()
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	result := make(chan error, 1)
	go func() { result <- runtime.Start(context.Background()) }()
	raw.waitStarted(t)
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("second Start succeeded")
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("first Start = %v", err)
	}
}

func TestConsumerRuntimeStopBeforeStartPreventsConsumption(t *testing.T) {
	raw := newBlockingConsumer()
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if raw.started() {
		t.Fatal("consumer started after runtime was stopped")
	}
}

func TestConsumerRuntimeStopHonorsDeadline(t *testing.T) {
	raw := newManuallyReleasedConsumer()
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	result := make(chan error, 1)
	go func() { result <- runtime.Start(context.Background()) }()
	raw.waitStarted(t)
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Stop(stopCtx); !errors.Is(err, context.Canceled) {
		raw.release()
		t.Fatalf("Stop error = %v, want canceled", err)
	}
	raw.release()
	raw.waitExited(t)
	if err := <-result; err != nil {
		t.Fatalf("Start after release = %v", err)
	}
}

func TestConsumerRunStatePreservesWrappedCanceledWhenConsumerWinsRace(t *testing.T) {
	var state consumerRunState
	canceledByRuntime := state.consumerReturned()
	if state.cancellationInitiated() {
		t.Fatal("late runtime cancellation won after Consumer returned")
	}
	wantErr := errors.New("spontaneous")
	err := normalizeConsumerError(fmt.Errorf("consumer: %w: %w", context.Canceled, wantErr), canceledByRuntime)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, wantErr) {
		t.Fatalf("normalized error = %v", err)
	}
}

func TestConsumerRunStateNormalizesCanceledWhenRuntimeWinsRace(t *testing.T) {
	var state consumerRunState
	if !state.cancellationInitiated() {
		t.Fatal("runtime cancellation was not recorded")
	}
	canceledByRuntime := state.consumerReturned()
	if err := normalizeConsumerError(fmt.Errorf("consumer: %w", context.Canceled), canceledByRuntime); err != nil {
		t.Fatalf("normalized error = %v", err)
	}
}

func TestConsumerRuntimePreservesSpontaneousCanceledWhenStartContextCancelsAfterReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wantErr := newObservedCanceledError()
	raw := &immediateErrorConsumer{err: wantErr}
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	startDone := make(chan error, 1)
	go func() { startDone <- runtime.Start(ctx) }()
	select {
	case <-wantErr.inspectionStarted:
	case <-time.After(time.Second):
		t.Fatal("Start did not classify the Consumer error")
	}
	cancel()
	close(wantErr.releaseInspection)
	if err := <-startDone; err != wantErr {
		t.Fatalf("Start error = %v, want original Consumer error", err)
	}
}

func TestConsumerRuntimePreservesParentDeadlineErrorAndCause(t *testing.T) {
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	raw := newContextErrorConsumer()
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error { return nil })
	err := runtime.Start(deadlineCtx)
	observed := <-raw.observed
	if !errors.Is(observed.err, context.DeadlineExceeded) || !errors.Is(observed.cause, context.DeadlineExceeded) {
		t.Fatalf("Consumer context err=%v cause=%v", observed.err, observed.cause)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start error = %v, want deadline exceeded", err)
	}
}
