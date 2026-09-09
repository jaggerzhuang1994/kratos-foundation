package kafka

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConsumerRecoveryBackoffGrowsAndResetsAfterCommit(t *testing.T) {
	logger, path := newQueueKafkaFileLogger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	creates := 0
	terminal := errors.New("stop test")
	value := newConsumer(ConsumerConfig{Concurrency: 1}, logger, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		creates++
		session, polls := creates, 0
		return &consumerClientStub{poll: func(context.Context, int) kgo.Fetches {
			polls++
			if session == 3 && polls == 1 {
				return testFetches(testRecord("orders", 0, 1))
			}
			if session == 4 {
				return kgo.NewErrFetch(terminal)
			}
			return kgo.NewErrFetch(io.EOF)
		}}, nil
	})
	if err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil }); !errors.Is(err, terminal) {
		t.Fatalf("Consume error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var attempts []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Kafka consumer reconnecting") {
			attempts = append(attempts, line)
		}
	}
	if len(attempts) != 3 {
		t.Fatalf("recovery logs = %v", attempts)
	}
	for index, expected := range []string{"attempt=1", "attempt=2", "attempt=1"} {
		if !strings.Contains(attempts[index], expected) {
			t.Fatalf("log %d = %s, want %s", index, attempts[index], expected)
		}
	}
}

func TestConsumerRecoveryWaitStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	failed := make(chan struct{})
	creates := 0
	client := &consumerClientStub{poll: func(context.Context, int) kgo.Fetches {
		close(failed)
		return kgo.NewErrFetch(io.EOF)
	}}
	value := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		creates++
		return client, nil
	})
	go func() {
		<-failed
		// 默认首次退避至少80ms；取消发生在等待期间，不能再创建客户端。
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	if !errors.Is(err, context.Canceled) || creates != 1 || client.closeCount() != 1 {
		t.Fatalf("err=%v creates=%d closes=%d", err, creates, client.closeCount())
	}
}

func TestConsumerRecoveryDoesNotCancelSiblingWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	creates := make(map[string]int)
	var clients []*consumerClientStub
	siblingPolling := make(chan struct{})
	value := newConsumer(ConsumerConfig{Concurrency: 2, Instance: "billing"}, nil,
		func(_ context.Context, _ string, instance string, _ ...kgo.Opt) (consumerClient, error) {
			mu.Lock()
			creates[instance]++
			generation := creates[instance]
			client := &consumerClientStub{poll: func(ctx context.Context, _ int) kgo.Fetches {
				if instance == "billing-1" {
					if generation == 1 {
						return kgo.NewErrFetch(io.EOF)
					}
					select {
					case <-siblingPolling:
					case <-ctx.Done():
						return kgo.NewErrFetch(ctx.Err())
					}
					if ctx.Err() != nil {
						t.Error("sibling was canceled before recovery")
					}
					cancel()
				} else {
					close(siblingPolling)
				}
				<-ctx.Done()
				return kgo.NewErrFetch(ctx.Err())
			}}
			clients = append(clients, client)
			mu.Unlock()
			return client, nil
		})
	err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume error = %v", err)
	}
	if creates["billing-1"] != 2 || creates["billing-2"] != 1 {
		t.Fatalf("client creations = %v", creates)
	}
	for _, client := range clients {
		if client.closeCount() != 1 {
			t.Fatalf("closes = %d", client.closeCount())
		}
	}
}

func TestConsumerRecoversTemporaryClientCreationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	creates := 0
	terminal := errors.New("stop test")
	client := &consumerClientStub{poll: func(context.Context, int) kgo.Fetches { return kgo.NewErrFetch(terminal) }}
	value := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		creates++
		if creates == 1 {
			return nil, io.EOF
		}
		return client, nil
	})
	err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
	if !errors.Is(err, terminal) || creates != 2 || client.closeCount() != 1 {
		t.Fatalf("err=%v creates=%d closes=%d", err, creates, client.closeCount())
	}
}
