package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type singleDeliveryConsumer struct {
	delivery Delivery
	mu       sync.Mutex
	acked    bool
}

func newSingleDeliveryConsumer(delivery Delivery) *singleDeliveryConsumer {
	return &singleDeliveryConsumer{delivery: delivery}
}

func (c *singleDeliveryConsumer) Consume(ctx context.Context, handler DeliveryHandler) error {
	err := handler(ctx, c.delivery)
	if err == nil {
		c.mu.Lock()
		c.acked = true
		c.mu.Unlock()
	}
	return err
}

func (c *singleDeliveryConsumer) acknowledged() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acked
}

func newTestConsumerRuntime(
	t *testing.T,
	consumer Consumer,
	deadLetter Producer,
	handler Handler,
) *ConsumerRuntime {
	t.Helper()
	return newTestConsumerRuntimeWithObservability(
		t,
		consumer,
		deadLetter,
		newDisabledTestObservability(t),
		handler,
	)
}

func newTestConsumerRuntimeWithObservability(
	t *testing.T,
	consumer Consumer,
	deadLetter Producer,
	observability Observability,
	handler Handler,
) *ConsumerRuntime {
	t.Helper()
	deadLetterDestination := ""
	if deadLetter != nil {
		deadLetterDestination = "orders.created.dlq"
	}
	runtime, err := NewConsumerRuntime(
		RuntimeConfig{
			Name:                  "order-created",
			Destination:           "orders.created",
			Retry:                 &RetryPolicy{MaxAttempts: 2},
			DeadLetter:            deadLetter,
			DeadLetterDestination: deadLetterDestination,
		},
		consumer,
		handler,
		observability,
	)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type blockingConsumer struct {
	startOnce sync.Once
	startCh   chan struct{}
	exitedCh  chan struct{}
	isStarted atomic.Bool
}

func newBlockingConsumer() *blockingConsumer {
	return &blockingConsumer{startCh: make(chan struct{}), exitedCh: make(chan struct{})}
}

func (c *blockingConsumer) Consume(ctx context.Context, _ DeliveryHandler) error {
	c.isStarted.Store(true)
	c.startOnce.Do(func() { close(c.startCh) })
	<-ctx.Done()
	close(c.exitedCh)
	return ctx.Err()
}

func (c *blockingConsumer) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-c.startCh:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
}

func (c *blockingConsumer) started() bool { return c.isStarted.Load() }

type manuallyReleasedConsumer struct {
	startCh     chan struct{}
	releaseCh   chan struct{}
	exitedCh    chan struct{}
	releaseOnce sync.Once
}

type immediateErrorConsumer struct {
	err error
}

func (c *immediateErrorConsumer) Consume(context.Context, DeliveryHandler) error { return c.err }

type observedContextResult struct {
	err   error
	cause error
}

type contextErrorConsumer struct {
	observed chan observedContextResult
}

type heartbeatSentinelError struct{}

func (*heartbeatSentinelError) Error() string { return "heartbeat failed" }

type joinedCancellationConsumer struct {
	started chan struct{}
	err     error
}

func (c *joinedCancellationConsumer) Consume(ctx context.Context, _ DeliveryHandler) error {
	close(c.started)
	<-ctx.Done()
	return errors.Join(ctx.Err(), c.err)
}

type panickingConsumer struct {
	captured chan context.Context
}

func (c *panickingConsumer) Consume(ctx context.Context, _ DeliveryHandler) error {
	c.captured <- ctx
	panic("raw consumer panic")
}

func newContextErrorConsumer() *contextErrorConsumer {
	return &contextErrorConsumer{observed: make(chan observedContextResult, 1)}
}

func (c *contextErrorConsumer) Consume(ctx context.Context, _ DeliveryHandler) error {
	<-ctx.Done()
	result := observedContextResult{err: ctx.Err(), cause: context.Cause(ctx)}
	c.observed <- result
	return result.err
}

type observedCanceledError struct {
	inspectionStarted chan struct{}
	releaseInspection chan struct{}
	startOnce         sync.Once
}

func newObservedCanceledError() *observedCanceledError {
	return &observedCanceledError{
		inspectionStarted: make(chan struct{}),
		releaseInspection: make(chan struct{}),
	}
}

func (e *observedCanceledError) Error() string { return "spontaneous consumer cancellation" }

func (e *observedCanceledError) Is(target error) bool {
	if target != context.Canceled {
		return false
	}
	e.startOnce.Do(func() { close(e.inspectionStarted) })
	<-e.releaseInspection
	return true
}

func newManuallyReleasedConsumer() *manuallyReleasedConsumer {
	return &manuallyReleasedConsumer{
		startCh:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		exitedCh:  make(chan struct{}),
	}
}

func (c *manuallyReleasedConsumer) Consume(ctx context.Context, _ DeliveryHandler) error {
	close(c.startCh)
	<-c.releaseCh
	close(c.exitedCh)
	return ctx.Err()
}

func (c *manuallyReleasedConsumer) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-c.startCh:
	case <-time.After(time.Second):
		t.Fatal("consumer did not start")
	}
}

func (c *manuallyReleasedConsumer) release() {
	c.releaseOnce.Do(func() { close(c.releaseCh) })
}

func (c *manuallyReleasedConsumer) waitExited(t *testing.T) {
	t.Helper()
	select {
	case <-c.exitedCh:
	case <-time.After(time.Second):
		t.Fatal("consumer did not exit")
	}
}
