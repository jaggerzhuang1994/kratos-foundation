package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
		func(_ context.Context, delivery Delivery) error {
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
	err := processFetches(context.Background(), committer, fetches, func(context.Context, Delivery) error {
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
		func(context.Context, Delivery) error {
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
			err := processFetches(test.ctx(), committer, test.fetches, func(context.Context, Delivery) error {
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
	value := newConsumer(ConsumerConfig{Connection: "main", Group: "billing"}, logger, nil).(*consumer)
	value.logFetchEvents("worker-1", kgo.Fetches{{Topics: []kgo.FetchTopic{{
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
		"module=kafka", "connection=main", "group=billing", "consumer=worker-1", "topic=orders", "partition=1",
		"Kafka reported data loss while fetching records",
		"Kafka consumer group session was lost",
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
	if err := processFetches(context.Background(), nil, nil, func(context.Context, Delivery) error { return nil }); err == nil {
		t.Fatal("processFetches accepted nil committer")
	}
	if err := processFetches(context.Background(), committer, testFetches(), func(context.Context, Delivery) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if committer.commitCount() != 0 {
		t.Fatalf("empty fetch commits = %d, want 0", committer.commitCount())
	}
}

// benchmarkCommitter 不模拟网络；用于隔离投递和整批提交前的本地处理开销。
type benchmarkCommitter struct{}

func (benchmarkCommitter) CommitRecords(context.Context, ...*kgo.Record) error { return nil }

func BenchmarkProcessFetches(b *testing.B) {
	for _, count := range []int{1, 32, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			records := make([]*kgo.Record, count)
			for i := range records {
				records[i] = testRecord("orders", 0, int64(i))
			}
			fetches := testFetches(records...)
			handler := func(context.Context, Delivery) error { return nil }
			b.ReportAllocs()
			for b.Loop() {
				if err := processFetches(context.Background(), benchmarkCommitter{}, fetches, handler); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestProcessFetchesPreservesMultiplePartitionsAndBrokers(t *testing.T) {
	first := testRecord("orders", 0, 1)
	second := testRecord("orders", 1, 3)
	third := testRecord("events", 0, 5)
	fetches := testFetches(first)
	fetches[0].Topics[0].Partitions = append(fetches[0].Topics[0].Partitions, kgo.FetchPartition{Partition: 1, Records: []*kgo.Record{second}})
	fetches = append(fetches, kgo.Fetch{Topics: []kgo.FetchTopic{{Topic: "events", Partitions: []kgo.FetchPartition{{Records: []*kgo.Record{third}}}}}})
	committer := newRecordingCommitter(nil)
	var ids []string
	if err := processFetches(context.Background(), committer, fetches, func(_ context.Context, d Delivery) error { ids = append(ids, d.Message.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"orders:0:1", "orders:1:3", "events:0:5"}) || !reflect.DeepEqual(committer.committedRecords(), []*kgo.Record{first, second, third}) {
		t.Fatalf("ids=%v records=%v", ids, committer.committedRecords())
	}
}

// externalCommitter 记录真实同步提交耗时，测量包含 Broker 往返。
type externalCommitter struct {
	client  *kgo.Client
	elapsed []time.Duration
}

func (c *externalCommitter) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	start := time.Now()
	err := c.client.CommitRecords(ctx, records...)
	c.elapsed = append(c.elapsed, time.Since(start))
	return err
}

func BenchmarkExternalKafkaBatch(b *testing.B) {
	if os.Getenv("FOUNDATION_TEST_KAFKA_ADDR") == "" {
		b.Skip("requires isolated Kafka")
	}
	for _, batch := range []int{1, 32, 128} {
		b.Run(fmt.Sprint(batch), func(b *testing.B) {
			factory, _, topic := externalKafkaFactory(b)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			producer, cleanup, err := NewProducer(factory, ProducerConfig{Connection: "external", Topic: topic})
			if err != nil {
				b.Fatal(err)
			}
			defer cleanup()
			messages := make([]*Message, b.N*batch+batch)
			random := rand.New(rand.NewPCG(1, 2))
			for i := range messages {
				body := make([]byte, 1024)
				for offset := 0; offset < len(body); offset += 8 {
					binary.LittleEndian.PutUint64(body[offset:], random.Uint64())
				}
				messages[i] = &Message{Body: body}
			}
			if err := producer.PublishBatch(ctx, messages); err != nil {
				b.Fatal(err)
			}
			client, err := factory.NewConsumerClient("external", consumerClientOptions(ConsumerConfig{Topic: topic, Group: topic, StartPosition: StartEarliest}, topic)...)
			if err != nil {
				b.Fatal(err)
			}
			defer client.CloseAllowingRebalance()
			committer := &externalCommitter{client: client, elapsed: make([]time.Duration, 0, b.N)}
			handler := func(context.Context, Delivery) error { return nil }
			// 首次入组与预热提交不计入稳定批次计时。
			warm := client.PollRecords(ctx, batch)
			if err := processFetches(ctx, client, warm, handler); err != nil {
				b.Fatal(err)
			}
			client.AllowRebalance()
			records := 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fetches := client.PollRecords(ctx, batch)
				if err := processFetches(ctx, committer, fetches, handler); err != nil {
					b.Fatal(err)
				}
				records += fetches.NumRecords()
				client.AllowRebalance()
			}
			b.StopTimer()
			if len(committer.elapsed) == 0 {
				b.Fatal("no successful commits")
			}
			slices.Sort(committer.elapsed)
			b.ReportMetric(float64(records)/b.Elapsed().Seconds(), "records/s")
			b.ReportMetric(float64(records)/float64(b.N), "records/batch")
			b.ReportMetric(float64(committer.elapsed[(len(committer.elapsed)-1)*95/100].Microseconds()), "commit-p95-us")
			b.ReportMetric(float64(committer.elapsed[(len(committer.elapsed)-1)*99/100].Microseconds()), "commit-p99-us")
		})
	}
}

func TestGroupSessionAuthenticationAndClosureRemainFatal(t *testing.T) {
	for _, failure := range []error{kerr.GroupAuthorizationFailed, kerr.SaslAuthenticationFailed, kgo.ErrClientClosed} {
		err := classifyFetchErrors(context.Background(), []kgo.FetchError{{Err: &kgo.ErrGroupSession{Err: failure}}})
		if !errors.Is(err, failure) {
			t.Fatalf("group failure=%v, want %v", err, failure)
		}
	}
}

func TestGroupSessionPreservesPermanentNonProtocolFailures(t *testing.T) {
	for _, failure := range []error{&kgo.ErrFirstReadEOF{}, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, errors.New("invalid group configuration"), errors.Join(context.Canceled, kerr.GroupAuthorizationFailed)} {
		err := classifyFetchErrors(context.Background(), []kgo.FetchError{{Err: &kgo.ErrGroupSession{Err: failure}}})
		if !errors.Is(err, failure) {
			t.Errorf("group failure=%v, want %v", err, failure)
		}
	}
	for _, failure := range []error{io.EOF, kerr.RebalanceInProgress, context.Canceled, context.DeadlineExceeded} {
		if err := classifyFetchErrors(context.Background(), []kgo.FetchError{{Err: &kgo.ErrGroupSession{Err: failure}}}); err != nil {
			t.Errorf("temporary group event should continue: %v", err)
		}
	}
}
