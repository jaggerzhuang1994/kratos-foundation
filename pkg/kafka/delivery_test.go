package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func TestConsumerRuntimeRetriesThenDeadLettersAndAcknowledges(t *testing.T) {
	delivery := Delivery{Message: &Message{ID: "message-1", Body: []byte("payload")}}
	raw := newSingleDeliveryConsumer(delivery)
	deadLetter := newRecordingProducer()
	observability := newMetricTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	attempts := 0
	runtime, err := NewConsumerRuntime(
		RuntimeConfig{
			Name:                  "order-created",
			Destination:           "orders.created",
			Retry:                 &RetryPolicy{MaxAttempts: 2},
			DeadLetter:            deadLetter,
			DeadLetterDestination: "orders.created.dlq",
		},
		raw,
		func(context.Context, *Message) error {
			attempts++
			return errors.New("dead-letter-secret")
		},
		observability,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !raw.acknowledged() {
		t.Fatalf("attempts=%d acknowledged=%v", attempts, raw.acknowledged())
	}
	got := deadLetter.single()
	if got.ID == "" || got.ID == "message-1" || got.Timestamp.IsZero() || string(got.Body) != "payload" {
		t.Fatalf("dead letter = %#v", got)
	}
	if headerValue(got.Headers, "x-queue-error") != "retry_exhausted" {
		t.Fatalf("dead-letter headers = %#v", got.Headers)
	}
	written := logs.String()
	if !strings.Contains(written, "kafka.consume.dead_lettered") || strings.Contains(written, "dead-letter-secret") {
		t.Fatalf("dead-letter log = %q", written)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_messages_total", map[string]string{"kafka_result": "dead_lettered"}); sample.counter != 1 {
		t.Fatalf("final message count = %v", sample.counter)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_message_duration_seconds", map[string]string{"kafka_result": "dead_lettered"}); sample.histogramCount != 1 {
		t.Fatalf("final duration count = %d", sample.histogramCount)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_dead_letters_total", map[string]string{"kafka_result": "success"}); sample.counter != 1 {
		t.Fatalf("dead-letter success count = %v", sample.counter)
	}
}

func TestConsumerRuntimeWithoutDeadLetterFailsAndDoesNotAcknowledge(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{ID: "message-1"}})
	wantErr := errors.New("poison")
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error {
		return Permanent(wantErr)
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, wantErr) || raw.acknowledged() {
		t.Fatalf("Start error=%v acknowledged=%v", err, raw.acknowledged())
	}
}

func TestConsumerRuntimeDecodeErrorSkipsHandlerAndUsesPermanentDeadLetter(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{
		Message: &Message{ID: "message-1", Body: []byte("raw")},
		Err:     errors.New("invalid encoding"),
	})
	deadLetter := newRecordingProducer()
	handlerCalls := 0
	runtime := newTestConsumerRuntime(t, raw, deadLetter, func(context.Context, *Message) error {
		handlerCalls++
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if handlerCalls != 0 || !raw.acknowledged() {
		t.Fatalf("handler calls=%d acknowledged=%v", handlerCalls, raw.acknowledged())
	}
	if got := headerValue(deadLetter.single().Headers, "x-queue-error"); got != "permanent" {
		t.Fatalf("dead-letter error = %q, want permanent", got)
	}
}

func TestConsumerRuntimeInvalidDeliverySkipsHandlerAndUsesPermanentDeadLetter(t *testing.T) {
	tests := []struct {
		name     string
		delivery Delivery
	}{
		{name: "nil message", delivery: Delivery{}},
		{
			name: "invalid header",
			delivery: Delivery{Message: &Message{
				ID:      "message-1",
				Headers: []Header{{Key: " ", Value: []byte("invalid")}},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := newSingleDeliveryConsumer(test.delivery)
			deadLetter := newRecordingProducer()
			handlerCalls := 0
			runtime := newTestConsumerRuntime(t, raw, deadLetter, func(context.Context, *Message) error {
				handlerCalls++
				return nil
			})
			if err := runtime.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if handlerCalls != 0 || !raw.acknowledged() {
				t.Fatalf("handler calls=%d acknowledged=%v", handlerCalls, raw.acknowledged())
			}
			dead := deadLetter.single()
			if got := headerValue(dead.Headers, "x-queue-error"); got != "permanent" {
				t.Fatalf("dead-letter error = %q", got)
			}
			if err := validateHeaders(dead.Headers); err != nil {
				t.Fatalf("dead-letter headers remained invalid: %v", err)
			}
		})
	}
}

func TestConsumerRuntimeGivesEveryAttemptAnIndependentMessage(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{Body: []byte("original")}})
	attempt := 0
	runtime := newTestConsumerRuntime(t, raw, nil, func(_ context.Context, message *Message) error {
		attempt++
		if attempt == 1 {
			message.Body[0] = 'X'
			return errors.New("retry")
		}
		if string(message.Body) != "original" {
			t.Fatalf("second attempt body = %q", message.Body)
		}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerRuntimeRetriesHandlerPanic(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{Message: new(Message)})
	attempt := 0
	runtime := newTestConsumerRuntime(t, raw, nil, func(context.Context, *Message) error {
		attempt++
		if attempt == 1 {
			panic("boom")
		}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempt != 2 {
		t.Fatalf("attempts = %d, want 2", attempt)
	}
}

func TestConsumerRuntimeCancellationDoesNotPublishDeadLetter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	raw := newSingleDeliveryConsumer(Delivery{Message: new(Message)})
	deadLetter := newRecordingProducer()
	runtime := newTestConsumerRuntime(t, raw, deadLetter, func(context.Context, *Message) error {
		cancel()
		return errors.New("interrupted")
	})
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start cancellation error = %v", err)
	}
	if deadLetter.publishCount() != 0 {
		t.Fatal("cancellation published a dead letter")
	}
}

func TestConsumerRuntimeDeadLetterFailureLeavesOriginalUnacknowledged(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{ID: "message-1"}})
	publishErr := errors.New("dead-letter unavailable")
	deadLetter := &recordingProducer{publishErr: publishErr}
	observability := newMetricTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	runtime := newTestConsumerRuntimeWithObservability(t, raw, deadLetter, observability, func(context.Context, *Message) error {
		return Permanent(errors.New("dead-letter-secret"))
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, publishErr) || raw.acknowledged() {
		t.Fatalf("error=%v acknowledged=%v", err, raw.acknowledged())
	}
	written := logs.String()
	if !strings.Contains(written, "kafka.dead_letter.failed") || strings.Contains(written, "dead-letter-secret") {
		t.Fatalf("dead-letter failure log = %q", written)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_dead_letters_total", map[string]string{"kafka_result": "error"}); sample.counter != 1 {
		t.Fatalf("dead-letter failure count = %v", sample.counter)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_runtime_failures_total", map[string]string{"kafka_result": "error"}); sample.counter != 1 {
		t.Fatalf("runtime failure count = %v", sample.counter)
	}
}

func TestConsumerRuntimeExtractsPropagationAndRetriesInsideOneConsumerSpan(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatal(err)
	}
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{
		ID: "message-1",
		Headers: []Header{
			{Key: "traceparent", Value: []byte("00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01")},
			{Key: "baggage", Value: []byte("tenant=acme")},
		},
	}})
	observability, exporter := newTestObservability(t)
	attempts := 0
	runtime := newTestConsumerRuntimeWithObservability(t, raw, nil, observability, func(ctx context.Context, _ *Message) error {
		attempts++
		if got := baggage.FromContext(ctx).Member("tenant").Value(); got != "acme" {
			t.Fatalf("baggage tenant = %q", got)
		}
		if got := trace.SpanContextFromContext(ctx).TraceID(); got != traceID {
			t.Fatalf("handler trace ID = %s", got)
		}
		if attempts == 1 {
			return errors.New("retry")
		}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	consumeIndex := -1
	consumeCount := 0
	for index, span := range spans {
		if span.Name == "kafka.consume" {
			consumeIndex = index
			consumeCount++
		}
	}
	if consumeCount != 1 {
		t.Fatalf("kafka.consume spans = %d, want 1", consumeCount)
	}
	span := spans[consumeIndex]
	if span.SpanKind != trace.SpanKindConsumer || span.Parent.TraceID() != traceID || span.Parent.SpanID() != spanID {
		t.Fatalf("span kind=%v parent=%s/%s", span.SpanKind, span.Parent.TraceID(), span.Parent.SpanID())
	}
	attemptEvents := 0
	retryEvents := 0
	for _, event := range span.Events {
		switch event.Name {
		case "kafka.consume.attempt":
			attemptEvents++
		case "kafka.consume.retry":
			retryEvents++
		}
	}
	if attemptEvents != 2 || retryEvents != 1 {
		t.Fatalf("attempt events=%d retry events=%d", attemptEvents, retryEvents)
	}
}

func TestConsumerRuntimeRecordsControlledFinalClassificationEvents(t *testing.T) {
	tests := []struct {
		name             string
		delivery         Delivery
		deadLetter       bool
		handlerError     error
		wantClass        string
		wantAttempts     int64
		wantHandlerCalls int
		secret           string
	}{
		{
			name:             "permanent handler failure",
			delivery:         Delivery{Message: &Message{ID: "message-1"}},
			handlerError:     Permanent(errors.New("permanent-secret")),
			wantClass:        "permanent",
			wantAttempts:     1,
			wantHandlerCalls: 1,
			secret:           "permanent-secret",
		},
		{
			name:             "retry exhausted",
			delivery:         Delivery{Message: &Message{ID: "message-1"}},
			handlerError:     errors.New("retry-secret"),
			wantClass:        "retry_exhausted",
			wantAttempts:     2,
			wantHandlerCalls: 2,
			secret:           "retry-secret",
		},
		{
			name:             "delivery decode failure",
			delivery:         Delivery{Message: &Message{ID: "message-1"}, Err: errors.New("decode-secret")},
			deadLetter:       true,
			wantClass:        "permanent",
			wantAttempts:     1,
			wantHandlerCalls: 0,
			secret:           "decode-secret",
		},
		{
			name: "invalid delivery header",
			delivery: Delivery{Message: &Message{
				ID:      "message-1",
				Headers: []Header{{Key: " ", Value: []byte("header-secret")}},
			}},
			deadLetter:       true,
			wantClass:        "permanent",
			wantAttempts:     1,
			wantHandlerCalls: 0,
			secret:           "header-secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := newSingleDeliveryConsumer(test.delivery)
			var deadLetter Producer
			if test.deadLetter {
				deadLetter = newRecordingProducer()
			}
			observability, exporter := newTestObservability(t)
			handlerCalls := 0
			runtime := newTestConsumerRuntimeWithObservability(
				t,
				raw,
				deadLetter,
				observability,
				func(context.Context, *Message) error {
					handlerCalls++
					return test.handlerError
				},
			)
			err := runtime.Start(context.Background())
			if test.deadLetter && err != nil {
				t.Fatal(err)
			}
			if !test.deadLetter && err == nil {
				t.Fatal("Start unexpectedly succeeded")
			}
			if handlerCalls != test.wantHandlerCalls {
				t.Fatalf("handler calls = %d, want %d", handlerCalls, test.wantHandlerCalls)
			}

			span := exporter.GetSpans()[0]
			wantEvent := "kafka.consume." + test.wantClass
			found := false
			for _, event := range span.Events {
				if event.Name != wantEvent {
					continue
				}
				found = true
				attemptsFound := false
				for _, field := range event.Attributes {
					if string(field.Key) == "kafka.attempts" {
						attemptsFound = true
						if field.Value.AsInt64() != test.wantAttempts {
							t.Fatalf("classification attempts = %d, want %d", field.Value.AsInt64(), test.wantAttempts)
						}
					}
					if strings.Contains(field.Value.Emit(), test.secret) {
						t.Fatalf("classification event leaked %q: %#v", test.secret, event)
					}
				}
				if !attemptsFound {
					t.Fatalf("classification event = %#v, want kafka.attempts", event)
				}
			}
			if !found {
				t.Fatalf("span events = %#v, want %q", span.Events, wantEvent)
			}
		})
	}
}

func TestConsumerRuntimeRetryObservabilityUsesStableNonSensitiveFields(t *testing.T) {
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{
		ID:      "message-1",
		Body:    []byte("body-secret"),
		Headers: []Header{{Key: "authorization", Value: []byte("header-secret")}},
	}})
	observability := newMetricTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	attempt := 0
	runtime := newTestConsumerRuntimeWithObservability(t, raw, nil, observability, func(context.Context, *Message) error {
		attempt++
		if attempt == 1 {
			return errors.New("body-secret header-secret trace-secret")
		}
		return nil
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	written := logs.String()
	if !strings.Contains(written, "kafka.consume.retry") || strings.Contains(written, "body-secret") || strings.Contains(written, "header-secret") || strings.Contains(written, "trace-secret") {
		t.Fatalf("retry log = %q", written)
	}
	labels := gatheredQueueMetricLabels(t, observability.Metrics, "kafka_consumer_attempts_total")
	if labels["kafka_destination"] != "orders.created" || labels["kafka_consumer"] != "order-created" {
		t.Fatalf("metric labels = %#v", labels)
	}
	if _, exists := labels["message_id"]; exists {
		t.Fatal("consumer metric contains message_id")
	}
}

func TestConsumerRuntimePermanentFailureLogsOnlyControlledFieldsAndRunsOnce(t *testing.T) {
	wantErr := errors.New("body-secret header-secret trace-secret")
	raw := newSingleDeliveryConsumer(Delivery{Message: &Message{ID: "message-1"}})
	observability := newMetricTestObservability(t)
	logs := newRecordingLogger()
	observability.Logger = logs
	handlerCalls := 0
	runtime := newTestConsumerRuntimeWithObservability(t, raw, nil, observability, func(context.Context, *Message) error {
		handlerCalls++
		return Permanent(wantErr)
	})
	err := runtime.Start(context.Background())
	if !errors.Is(err, wantErr) || handlerCalls != 1 || raw.acknowledged() {
		t.Fatalf("Start error=%v handler calls=%d acknowledged=%v", err, handlerCalls, raw.acknowledged())
	}
	written := logs.String()
	if !strings.Contains(written, "kafka.consume.failed") || strings.Contains(written, "body-secret") || strings.Contains(written, "header-secret") || strings.Contains(written, "trace-secret") {
		t.Fatalf("failure log = %q", written)
	}
	if !strings.Contains(written, "kafka.consumer.failed") {
		t.Fatalf("runtime failure log = %q", written)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_messages_total", map[string]string{"kafka_result": "error"}); sample.counter != 1 {
		t.Fatalf("final failure count = %v", sample.counter)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_message_duration_seconds", map[string]string{"kafka_result": "error"}); sample.histogramCount != 1 {
		t.Fatalf("final failure duration count = %d", sample.histogramCount)
	}
	if sample := findQueueMetricSample(t, observability.Metrics, "kafka_consumer_runtime_failures_total", map[string]string{"kafka_result": "error"}); sample.counter != 1 {
		t.Fatalf("runtime failure count = %v", sample.counter)
	}
}

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
