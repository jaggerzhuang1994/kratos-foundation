package kafka

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func testRecord(topic string, partition int32, offset int64) *kgo.Record {
	return &kgo.Record{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
		Key:       []byte("key"),
		Value:     []byte("value"),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: []byte("header")},
		},
		Timestamp: time.Unix(offset, 0),
	}
}

func testFetches(records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "orders",
			Partitions: []kgo.FetchPartition{{
				Partition: 0,
				Records:   records,
			}},
		}},
	}}
}

type recordingCommitter struct {
	mu      sync.Mutex
	records []*kgo.Record
	err     error
	count   int
}

type consumerLifecycleFactory struct {
	mu            sync.Mutex
	want          int
	fatalInstance string
	fatalErr      error
	allCreated    chan struct{}
	createdOnce   sync.Once
	instances     []string
	clients       []*consumerClientStub
}

func newConsumerLifecycleFactory(
	want int,
	fatalInstance string,
	fatalErr error,
) *consumerLifecycleFactory {
	return &consumerLifecycleFactory{
		want:          want,
		fatalInstance: fatalInstance,
		fatalErr:      fatalErr,
		allCreated:    make(chan struct{}),
		clients:       make([]*consumerClientStub, 0, want),
	}
}

func (f *consumerLifecycleFactory) create(
	_ context.Context,
	connection string,
	instance string,
	_ ...kgo.Opt,
) (consumerClient, error) {
	if connection != "main" {
		return nil, errors.New("unexpected connection")
	}
	client := &consumerClientStub{}
	client.poll = func(ctx context.Context, _ int) kgo.Fetches {
		select {
		case <-f.allCreated:
		case <-ctx.Done():
			return kgo.NewErrFetch(ctx.Err())
		}
		if instance == f.fatalInstance {
			return kgo.NewErrFetch(f.fatalErr)
		}
		<-ctx.Done()
		return kgo.NewErrFetch(ctx.Err())
	}
	f.mu.Lock()
	f.instances = append(f.instances, instance)
	f.clients = append(f.clients, client)
	if len(f.clients) == f.want {
		f.createdOnce.Do(func() { close(f.allCreated) })
	}
	f.mu.Unlock()
	return client, nil
}

func (f *consumerLifecycleFactory) snapshot() ([]string, []*consumerClientStub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	instances := append([]string(nil), f.instances...)
	clients := append([]*consumerClientStub(nil), f.clients...)
	return instances, clients
}

type consumerClientStub struct {
	mu             sync.Mutex
	poll           func(context.Context, int) kgo.Fetches
	closed         int
	allowRebalance int
}

func (c *consumerClientStub) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	return c.poll(ctx, maxRecords)
}

func (*consumerClientStub) CommitRecords(context.Context, ...*kgo.Record) error { return nil }

func (*consumerClientStub) LeaveGroupContext(context.Context) error { return nil }

func (c *consumerClientStub) AllowRebalance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowRebalance++
}

func (c *consumerClientStub) CloseAllowingRebalance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
}

func (c *consumerClientStub) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *consumerClientStub) allowRebalanceCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allowRebalance
}

func newRecordingCommitter(err error) *recordingCommitter {
	return &recordingCommitter{err: err}
}

func (c *recordingCommitter) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.records = append([]*kgo.Record(nil), records...)
	return c.err
}

func (c *recordingCommitter) commitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *recordingCommitter) committedRecords() []*kgo.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*kgo.Record(nil), c.records...)
}

type producerClientStub struct {
	mu     sync.Mutex
	closed int
}

func newProducerClientStub() *producerClientStub {
	return &producerClientStub{}
}

func (*producerClientStub) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	results := make(kgo.ProduceResults, len(records))
	for index, record := range records {
		results[index] = kgo.ProduceResult{Record: record}
	}
	return results
}

func (c *producerClientStub) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
}

func (c *producerClientStub) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
