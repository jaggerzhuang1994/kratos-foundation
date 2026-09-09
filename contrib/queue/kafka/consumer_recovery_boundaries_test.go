package kafka

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kgo"
)

type sessionBoundClient struct {
	*consumerClientStub
	ctx    context.Context
	t      *testing.T
	leaves int
}

func (c *sessionBoundClient) LeaveGroupContext(ctx context.Context) error {
	c.leaves++
	if err := c.ctx.Err(); err != nil {
		c.t.Errorf("SDK context canceled before LeaveGroup: %v", err)
	}
	if err := ctx.Err(); err != nil {
		c.t.Errorf("LeaveGroup received canceled context: %v", err)
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		c.t.Error("LeaveGroup must have a bounded shutdown deadline")
	}
	return nil
}

func (c *sessionBoundClient) CloseAllowingRebalance() {
	if !errors.Is(c.ctx.Err(), context.Canceled) {
		c.t.Error("SDK session must be canceled before CloseAllowingRebalance")
	}
	c.consumerClientStub.CloseAllowingRebalance()
}

func TestConsumerLeavesGroupBeforeCancelingSDKSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	terminal := errors.New("stop test")
	var sessions []*sessionBoundClient
	value := newConsumer(ConsumerConfig{Concurrency: 1}, nil,
		func(session context.Context, _, _ string, _ ...kgo.Opt) (consumerClient, error) {
			if len(sessions) > 0 && sessions[len(sessions)-1].ctx.Err() == nil {
				t.Error("replacement created before old SDK session canceled")
			}
			failure := error(io.EOF)
			if len(sessions) > 0 {
				failure = terminal
			}
			client := &sessionBoundClient{consumerClientStub: &consumerClientStub{poll: func(context.Context, int) kgo.Fetches { return kgo.NewErrFetch(failure) }}, ctx: session, t: t}
			sessions = append(sessions, client)
			return client, nil
		})
	if err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil }); !errors.Is(err, terminal) {
		t.Fatalf("Consume = %v", err)
	}
	if len(sessions) != 2 || sessions[0].ctx == sessions[1].ctx {
		t.Fatalf("expected two independent SDK sessions, got %v", sessions)
	}
	for _, client := range sessions {
		if client.closeCount() != 1 || client.leaves != 1 {
			t.Fatalf("closes=%d leaves=%d", client.closeCount(), client.leaves)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("worker cleanup canceled caller context: %v", ctx.Err())
	}
}

func TestConsumerCancellationKeepsSDKAliveUntilLeaveGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var client *sessionBoundClient
	value := newConsumer(ConsumerConfig{Concurrency: 1}, nil,
		func(session context.Context, _, _ string, _ ...kgo.Opt) (consumerClient, error) {
			client = &sessionBoundClient{ctx: session, t: t, consumerClientStub: &consumerClientStub{
				poll: func(ctx context.Context, _ int) kgo.Fetches {
					cancel()
					return kgo.NewErrFetch(ctx.Err())
				},
			}}
			return client, nil
		})
	if err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume = %v", err)
	}
	if client.leaves != 1 || client.closeCount() != 1 {
		t.Fatalf("leaves=%d closes=%d", client.leaves, client.closeCount())
	}
}

type blockedLeaveClient struct{ *sessionBoundClient }

func (c *blockedLeaveClient) LeaveGroupContext(ctx context.Context) error {
	_ = c.sessionBoundClient.LeaveGroupContext(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func TestConsumerShutdownBoundsUnavailableLeaveGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var client *blockedLeaveClient
		value := newConsumer(ConsumerConfig{Concurrency: 1}, nil,
			func(session context.Context, _, _ string, _ ...kgo.Opt) (consumerClient, error) {
				client = &blockedLeaveClient{&sessionBoundClient{ctx: session, t: t, consumerClientStub: &consumerClientStub{
					poll: func(ctx context.Context, _ int) kgo.Fetches {
						cancel()
						return kgo.NewErrFetch(ctx.Err())
					},
				}}}
				return client, nil
			})
		started := time.Now()
		err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
		if !errors.Is(err, context.Canceled) || client.leaves != 1 || client.closeCount() != 1 {
			t.Fatalf("Consume=%v leaves=%d closes=%d", err, client.leaves, client.closeCount())
		}
		if elapsed := time.Since(started); elapsed <= 0 || elapsed > 5*time.Second {
			t.Fatalf("shutdown elapsed=%s, expected a finite LeaveGroup wait", elapsed)
		}
	})
}

func TestConsumerRecoveryUsesEarliestOnlyForMissingCommittedOffsets(t *testing.T) {
	config := ConsumerConfig{Topic: "orders", Group: "billing", Instance: "billing-1", Concurrency: 1, StartPosition: queue.StartLatest}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	terminal := errors.New("stop test")
	var starts, resets []string
	creates := 0
	value := newConsumer(config, nil, func(session context.Context, _, instance string, options ...kgo.Opt) (consumerClient, error) {
		opts := append([]kgo.Opt{kgo.SeedBrokers("127.0.0.1:1"), kgo.WithContext(session)}, consumerClientOptions(config, instance)...)
		sdk, err := kgo.NewClient(append(opts, options...)...)
		if err != nil {
			t.Fatal(err)
		}
		starts = append(starts, sdk.OptValue(kgo.ConsumeStartOffset).(kgo.Offset).String())
		resets = append(resets, sdk.OptValue(kgo.ConsumeResetOffset).(kgo.Offset).String())
		sdk.Close()
		creates++
		if creates == 1 {
			return &recoveryClient{consumerClientStub: &consumerClientStub{poll: func(context.Context, int) kgo.Fetches { return testFetches(testRecord("orders", 0, 10)) }}, commit: func(context.Context, ...*kgo.Record) error { return io.EOF }}, nil
		}
		return &consumerClientStub{poll: func(context.Context, int) kgo.Fetches { return kgo.NewErrFetch(terminal) }}, nil
	})
	if err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil }); !errors.Is(err, terminal) {
		t.Fatalf("Consume=%v", err)
	}
	if len(starts) != 2 || starts[0] != kgo.NewOffset().AtEnd().String() || starts[1] != kgo.NewOffset().AtStart().String() {
		t.Fatalf("initial/recovery start offsets=%v", starts)
	}
	for _, reset := range resets {
		if reset != kgo.NewOffset().AtEnd().String() {
			t.Fatalf("recovery changed configured out-of-range reset: %v", resets)
		}
	}
}

func TestPublicConsumerFactoryBindsSDKContextAndRecoveryOptions(t *testing.T) {
	manager := newQueueKafkaManager(t, map[string]*config_pb.KafkaConnection{"main": {Brokers: []string{"127.0.0.1:1"}}})
	value, err := NewConsumer(manager, nil, ConsumerConfig{Connection: "main", Topic: "orders", Group: "billing", Instance: "billing-1", StartPosition: queue.StartLatest})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := value.(*consumer).createClient(ctx, "main", "billing-1", kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseAllowingRebalance()
	sdk := client.(*kgo.Client)
	if sdk.OptValue(kgo.WithContext) != ctx {
		t.Fatal("production factory did not bind SDK to worker context")
	}
	if sdk.OptValue(kgo.ConsumeStartOffset).(kgo.Offset).String() != kgo.NewOffset().AtStart().String() {
		t.Fatal("production factory discarded recovery offset option")
	}
	cancel()
}

func TestSDKConsumerShutdownCancelsInFlightDial(t *testing.T) {
	manager := newQueueKafkaManager(t, map[string]*config_pb.KafkaConnection{"main": {Brokers: []string{"127.0.0.1:1"}}})
	value, err := NewConsumer(manager, nil, ConsumerConfig{Connection: "main", Topic: "orders", Group: "billing", Instance: "billing-1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	exited := make(chan struct{}, 1)
	client, err := value.(*consumer).createClient(ctx, "main", "billing-1", kgo.Dialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case exited <- struct{}{}:
		default:
		}
		return nil, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseAllowingRebalance()
	polled := make(chan struct{})
	go func() { client.PollRecords(ctx, 1); close(polled) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("SDK did not start the blocking dial")
	}
	cancel()
	// 与 consumeClient 一致，先等待 Poll 返回再释放再均衡屏障并关闭。
	// 并发 Close 可能先 AllowRebalance，随后 Poll 再次设置屏障，使退组永久等待。
	for name, done := range map[string]<-chan struct{}{"dial": exited, "poll": polled} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("SDK %s did not finish after session cancellation", name)
		}
	}
	closed := make(chan struct{})
	go func() { client.CloseAllowingRebalance(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("SDK close did not finish after session cancellation")
	}
}
