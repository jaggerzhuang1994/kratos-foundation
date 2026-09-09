package redis

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestProducerPublishEncodesIndependentXAddCommand(t *testing.T) {
	hook := &producerCommandHook{}
	producer := newHookedProducer(t, hook)
	timestamp := time.Date(2026, time.August, 31, 10, 11, 12, 345, time.UTC)
	message := &queue.Message{
		ID:        "message-1",
		Key:       []byte("order-1"),
		Body:      []byte(`{"status":"paid"}`),
		Headers:   []queue.Header{{Key: "traceparent", Value: []byte("trace-1")}},
		Timestamp: timestamp,
	}
	wantMessage := message.Clone()

	if err := producer.Publish(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(message, wantMessage) {
		t.Fatalf("Publish mutated message: got %#v, want %#v", message, wantMessage)
	}
	commands, pipelines := hook.snapshot()
	if len(commands) != 1 || len(pipelines) != 0 {
		t.Fatalf("Redis calls = %d direct, %d pipelines; want 1, 0", len(commands), len(pipelines))
	}

	// A completed producer call must not leave its encoded command aliased to
	// caller-owned bytes. This also protects command-observing instrumentation.
	message.Key[0] = 'X'
	message.Body[0] = 'X'
	message.Headers[0].Value[0] = 'X'
	assertXAddCommand(t, commands[0], "orders", []string{"maxlen", "~", "100"}, wantMessage)
}

func TestProducerPublishValidatesStateAndWrapsRedisErrors(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		hook := &producerCommandHook{}
		producer := newHookedProducer(t, hook)
		err := producer.Publish(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "message is nil") {
			t.Fatalf("Publish(nil) error = %v", err)
		}
		commands, _ := hook.snapshot()
		if len(commands) != 0 {
			t.Fatalf("Publish(nil) issued %d Redis commands", len(commands))
		}
	})

	t.Run("uninitialized", func(t *testing.T) {
		var nilProducer *producer
		if err := nilProducer.Publish(context.Background(), &queue.Message{}); err == nil ||
			!strings.Contains(err.Error(), "not initialized") {
			t.Fatalf("nil producer error = %v", err)
		}
		value := &producer{}
		if err := value.Publish(context.Background(), &queue.Message{}); err == nil ||
			!strings.Contains(err.Error(), "not initialized") {
			t.Fatalf("nil client error = %v", err)
		}
	})

	t.Run("Redis failure", func(t *testing.T) {
		wantErr := errors.New("Redis rejected XADD")
		hook := &producerCommandHook{processErr: wantErr}
		producer := newHookedProducer(t, hook)
		err := producer.Publish(context.Background(), &queue.Message{ID: "message-1"})
		if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), `XADD "orders"`) {
			t.Fatalf("Publish Redis failure = %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		hook := &producerCommandHook{}
		producer := newHookedProducer(t, hook)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := producer.Publish(ctx, &queue.Message{ID: "message-1"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish canceled context error = %v", err)
		}
	})
}

func TestProducerPublishBatchPipelinesEveryEncodedMessage(t *testing.T) {
	hook := &producerCommandHook{}
	producer := newHookedProducer(t, hook)
	messages := []*queue.Message{
		{
			ID:        "message-1",
			Key:       []byte("order-1"),
			Body:      []byte("first"),
			Headers:   []queue.Header{{Key: "trace", Value: []byte("trace-1")}},
			Timestamp: time.Unix(100, 1),
		},
		{
			ID:        "message-2",
			Key:       []byte("order-2"),
			Body:      []byte("second"),
			Headers:   []queue.Header{{Key: "trace", Value: []byte("trace-2")}},
			Timestamp: time.Unix(200, 2),
		},
	}
	if err := producer.PublishBatch(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	commands, pipelines := hook.snapshot()
	if len(commands) != 0 || len(pipelines) != 1 || len(pipelines[0]) != len(messages) {
		t.Fatalf("Redis calls = %d direct, pipelines %#v", len(commands), pipelines)
	}
	for index, command := range pipelines[0] {
		assertXAddCommand(t, command, "orders", []string{"maxlen", "~", "100"}, messages[index])
	}
}

func TestProducerPublishBatchReportsPerCommandAndPipelineFailures(t *testing.T) {
	messages := []*queue.Message{{ID: "one"}, {ID: "two"}, {ID: "three"}}

	t.Run("per-command failures", func(t *testing.T) {
		firstErr := errors.New("first XADD failed")
		thirdErr := errors.New("third XADD failed")
		hook := &producerCommandHook{
			pipelineErr: firstErr,
			pipelineCommandErrs: map[int]error{
				0: firstErr,
				2: thirdErr,
			},
		}
		producer := newHookedProducer(t, hook)
		err := producer.PublishBatch(context.Background(), messages)
		var batchErr *queue.BatchError
		if !errors.As(err, &batchErr) || !errors.Is(err, firstErr) || !errors.Is(err, thirdErr) {
			t.Fatalf("PublishBatch per-command error = %v", err)
		}
		assertBatchFailures(t, batchErr.Failures, []queue.BatchFailure{
			{Index: 0, MessageID: "one", Err: firstErr},
			{Index: 2, MessageID: "three", Err: thirdErr},
		})
	})

	t.Run("pipeline-wide failure", func(t *testing.T) {
		pipelineErr := errors.New("pipeline connection failed")
		hook := &producerCommandHook{pipelineErr: pipelineErr}
		producer := newHookedProducer(t, hook)
		err := producer.PublishBatch(context.Background(), messages)
		var batchErr *queue.BatchError
		if !errors.As(err, &batchErr) || !errors.Is(err, pipelineErr) {
			t.Fatalf("PublishBatch pipeline error = %v", err)
		}
		assertBatchFailures(t, batchErr.Failures, []queue.BatchFailure{
			{Index: 0, MessageID: "one", Err: pipelineErr},
			{Index: 1, MessageID: "two", Err: pipelineErr},
			{Index: 2, MessageID: "three", Err: pipelineErr},
		})
	})

	t.Run("canceled context", func(t *testing.T) {
		hook := &producerCommandHook{}
		producer := newHookedProducer(t, hook)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := producer.PublishBatch(ctx, messages[:2])
		var batchErr *queue.BatchError
		if !errors.As(err, &batchErr) || !errors.Is(err, context.Canceled) || len(batchErr.Failures) != 2 {
			t.Fatalf("PublishBatch canceled context error = %v", err)
		}
	})
}

func TestProducerPublishBatchValidatesBeforeOpeningPipeline(t *testing.T) {
	var nilProducer *producer
	if err := nilProducer.PublishBatch(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("nil producer empty batch error = %v", err)
	}
	if err := (&producer{}).PublishBatch(context.Background(), []*queue.Message{{ID: "one"}}); err == nil ||
		!strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("nil client batch error = %v", err)
	}

	hook := &producerCommandHook{}
	producer := newHookedProducer(t, hook)
	if err := producer.PublishBatch(context.Background(), nil); err != nil {
		t.Fatalf("empty batch error = %v", err)
	}
	if err := producer.PublishBatch(context.Background(), []*queue.Message{{ID: "one"}, nil}); err == nil ||
		!strings.Contains(err.Error(), "message 1 is nil") {
		t.Fatalf("nil batch message error = %v", err)
	}
	_, pipelines := hook.snapshot()
	if len(pipelines) != 0 {
		t.Fatalf("invalid batches opened %d pipelines", len(pipelines))
	}
}

func TestCollectBatchFailuresCoversSuccessAndErrorSemantics(t *testing.T) {
	messages := []*queue.Message{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	successCommands := []goredis.Cmder{
		goredis.NewStringResult("1-0", nil),
		goredis.NewStringResult("1-1", nil),
		goredis.NewStringResult("1-2", nil),
	}

	t.Run("success", func(t *testing.T) {
		failures, err := collectBatchFailures(messages, successCommands, nil)
		if err != nil || len(failures) != 0 {
			t.Fatalf("collectBatchFailures success = (%#v, %v)", failures, err)
		}
	})

	t.Run("per-command error takes precedence over pipeline duplicate", func(t *testing.T) {
		commandErr := errors.New("second command failed")
		pipelineErr := errors.New("pipeline returned first command failure")
		commands := append([]goredis.Cmder(nil), successCommands...)
		commands[1] = goredis.NewStringResult("", commandErr)
		failures, err := collectBatchFailures(messages, commands, pipelineErr)
		if err != nil {
			t.Fatal(err)
		}
		assertBatchFailures(t, failures, []queue.BatchFailure{{
			Index: 1, MessageID: "two", Err: commandErr,
		}})
	})

	t.Run("pipeline-only error fans out", func(t *testing.T) {
		pipelineErr := errors.New("pipeline failed before replies")
		failures, err := collectBatchFailures(messages, successCommands, pipelineErr)
		if err != nil {
			t.Fatal(err)
		}
		assertBatchFailures(t, failures, []queue.BatchFailure{
			{Index: 0, MessageID: "one", Err: pipelineErr},
			{Index: 1, MessageID: "two", Err: pipelineErr},
			{Index: 2, MessageID: "three", Err: pipelineErr},
		})
	})
}

func TestCollectBatchFailuresRejectsMalformedCommandLists(t *testing.T) {
	messages := []*queue.Message{{ID: "one"}}
	for _, test := range []struct {
		name     string
		commands []goredis.Cmder
		want     string
	}{
		{name: "missing command", commands: nil, want: "0 commands for 1 messages"},
		{
			name: "extra command",
			commands: []goredis.Cmder{
				goredis.NewStringResult("1-0", nil),
				goredis.NewStringResult("1-1", nil),
			},
			want: "2 commands for 1 messages",
		},
		{name: "nil command", commands: []goredis.Cmder{nil}, want: "command 0 is nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures, err := collectBatchFailures(messages, test.commands, nil)
			if failures != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("collectBatchFailures malformed result = (%#v, %v)", failures, err)
			}
		})
	}
}
