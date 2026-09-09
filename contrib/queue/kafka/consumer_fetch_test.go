package kafka

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestProcessFetchesCommitsDecodedDeliveriesAfterEveryHandlerSucceeds(t *testing.T) {
	records := []*kgo.Record{
		testRecord("orders", 0, 1),
		testRecord("orders", 0, 2),
	}
	fetches := testFetches(records...)
	committer := newRecordingCommitter(nil)
	wantIDs := []string{"orders:0:1", "orders:0:2"}
	handled := 0
	err := processFetches(
		context.Background(),
		committer,
		fetches,
		func(_ context.Context, delivery queue.Delivery) error {
			if committer.commitCount() != 0 {
				t.Fatal("records committed before the full batch succeeded")
			}
			if delivery.Err != nil || delivery.Message == nil {
				t.Fatalf("delivery = %#v", delivery)
			}
			wantID := wantIDs[handled]
			if delivery.Message.ID != wantID ||
				string(delivery.Message.Key) != "key" ||
				string(delivery.Message.Body) != "value" ||
				len(delivery.Message.Headers) != 1 ||
				delivery.Message.Headers[0].Key != "trace" ||
				string(delivery.Message.Headers[0].Value) != "header" {
				t.Fatalf("delivery message = %#v, want id %q", delivery.Message, wantID)
			}
			handled++
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if handled != 2 || committer.commitCount() != 1 {
		t.Fatalf("handled=%d commits=%d", handled, committer.commitCount())
	}
	if got := committer.committedRecords(); len(got) != len(records) ||
		got[0] != records[0] || got[1] != records[1] {
		t.Fatalf("committed records = %#v", got)
	}
}

func TestProcessFetchesCommitsOnlyAfterEveryDeliverySucceeds(t *testing.T) {
	fetches := testFetches(testRecord("orders", 0, 1), testRecord("orders", 0, 2))
	committer := newRecordingCommitter(nil)
	handlerErr := errors.New("handler failed")
	handled := 0
	err := processFetches(context.Background(), committer, fetches, func(context.Context, queue.Delivery) error {
		handled++
		if handled == 2 {
			return handlerErr
		}
		return nil
	})
	if !errors.Is(err, handlerErr) || committer.commitCount() != 0 {
		t.Fatalf("error=%v commits=%d", err, committer.commitCount())
	}
}

func TestProcessFetchesDoesNotCommitWhenContextIsCanceledByHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	committer := newRecordingCommitter(nil)
	err := processFetches(
		ctx,
		committer,
		testFetches(testRecord("orders", 0, 1)),
		func(context.Context, queue.Delivery) error {
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) || committer.commitCount() != 0 {
		t.Fatalf("error=%v commits=%d", err, committer.commitCount())
	}
}

func TestProcessFetchesPreservesFetchContextAndCommitErrors(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	commitErr := errors.New("commit failed")
	tests := []struct {
		name      string
		ctx       func() context.Context
		fetches   kgo.Fetches
		commitErr error
		wantErr   error
	}{
		{
			name:    "fetch",
			ctx:     context.Background,
			fetches: kgo.NewErrFetch(fetchErr),
			wantErr: fetchErr,
		},
		{
			name: "context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			fetches: testFetches(testRecord("orders", 0, 1)),
			wantErr: context.Canceled,
		},
		{
			name:      "commit",
			ctx:       context.Background,
			fetches:   testFetches(testRecord("orders", 0, 1)),
			commitErr: commitErr,
			wantErr:   commitErr,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			committer := newRecordingCommitter(test.commitErr)
			err := processFetches(test.ctx(), committer, test.fetches, func(context.Context, queue.Delivery) error {
				return nil
			})
			if err == nil || err.Error() == "" {
				t.Fatalf("error = %v", err)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want chain %v", err, test.wantErr)
			}
			if test.name != "commit" && committer.commitCount() != 0 {
				t.Fatalf("commits = %d", committer.commitCount())
			}
		})
	}
}

func TestConsumerLogsRecoverableKafkaProtocolEvents(t *testing.T) {
	logger, logPath := newQueueKafkaFileLogger(t)
	value := newConsumer(ConsumerConfig{}, logger, nil).(*consumer)
	value.logFetchEvents(kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: "orders",
		Partitions: []kgo.FetchPartition{
			{Partition: 1, Err: &kgo.ErrDataLoss{Topic: "orders", Partition: 1, ConsumedTo: 10, ResetTo: 8}},
			{Partition: 2, Err: &kgo.ErrGroupSession{Err: errors.New("rebalance failed")}},
			{Partition: 3, Err: errors.New("ordinary fetch error")},
		},
	}}}})
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, fragment := range []string{
		"module=queue.kafka",
		"Kafka consumer data loss",
		"Kafka consumer group session lost",
		"orders[1]",
		"orders[2]",
	} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("consumer log lacks %q: %s", fragment, logs)
		}
	}
	if strings.Contains(logs, "ordinary fetch error") {
		t.Fatalf("consumer logged non-protocol fetch error twice: %s", logs)
	}
}

func TestClassifyFetchErrorsIgnoresRecoverableEventsAndJoinsFatalErrors(t *testing.T) {
	recoverable := []kgo.FetchError{
		{Topic: "orders", Partition: 1, Err: &kgo.ErrDataLoss{Topic: "orders", Partition: 1}},
		{Topic: "orders", Partition: 2, Err: &kgo.ErrGroupSession{Err: kerr.RebalanceInProgress}},
	}
	if err := classifyFetchErrors(context.Background(), recoverable); err != nil {
		t.Fatalf("recoverable events error = %v", err)
	}
	first := errors.New("first fatal fetch")
	second := errors.New("second fatal fetch")
	err := classifyFetchErrors(context.Background(), []kgo.FetchError{
		{Topic: "orders", Partition: 1, Err: first},
		{Topic: "orders", Partition: 2, Err: second},
	})
	if !errors.Is(err, first) || !errors.Is(err, second) || !strings.Contains(err.Error(), "orders[1]") || !strings.Contains(err.Error(), "orders[2]") {
		t.Fatalf("joined fetch error = %v", err)
	}
	if err := classifyFetchErrors(context.Background(), []kgo.FetchError{{Err: kgo.ErrClientClosed}}); !errors.Is(err, kgo.ErrClientClosed) {
		t.Fatalf("client closed error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := classifyFetchErrors(canceled, []kgo.FetchError{{Err: kgo.ErrClientClosed}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled client error = %v", err)
	}
}

func TestProcessFetchesValidatesBoundariesAndSkipsEmptyCommit(t *testing.T) {
	committer := newRecordingCommitter(nil)
	if err := processFetches(context.Background(), committer, nil, nil); err == nil {
		t.Fatal("processFetches accepted nil handler")
	}
	if err := processFetches(context.Background(), nil, nil, func(context.Context, queue.Delivery) error { return nil }); err == nil {
		t.Fatal("processFetches accepted nil committer")
	}
	if err := processFetches(context.Background(), committer, testFetches(), func(context.Context, queue.Delivery) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if committer.commitCount() != 0 {
		t.Fatalf("empty fetch commits = %d, want 0", committer.commitCount())
	}
}
