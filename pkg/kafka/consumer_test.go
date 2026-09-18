package kafka

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

type recoveryClient struct {
	*consumerClientStub
	commit func(context.Context, ...*kgo.Record) error
}

func (c *recoveryClient) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	return c.commit(ctx, records...)
}

func TestConsumerRecoversCommitFailureByReplayingUncommittedRecords(t *testing.T) {
	for _, failure := range []error{io.EOF, kerr.IllegalGeneration, kerr.UnknownMemberID, kerr.RebalanceInProgress} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			record := testRecord("orders", 0, 10)
			var clients []*recoveryClient
			creates, handled, commits := 0, 0, 0
			value := newConsumer(ConsumerConfig{Connection: "main", Concurrency: 1, MaxPollRecords: 1}, nil,
				func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
					creates++
					if len(clients) > 0 && clients[len(clients)-1].closeCount() != 1 {
						t.Error("replacement created before old client closed")
					}
					generation := creates
					client := &recoveryClient{consumerClientStub: &consumerClientStub{}}
					client.poll = func(ctx context.Context, _ int) kgo.Fetches {
						if ctx.Err() != nil {
							return kgo.NewErrFetch(ctx.Err())
						}
						return testFetches(record)
					}
					client.commit = func(context.Context, ...*kgo.Record) error {
						commits++
						if generation == 1 {
							return failure
						}
						cancel()
						return nil
					}
					clients = append(clients, client)
					return client, nil
				})
			err := value.Consume(ctx, func(context.Context, Delivery) error { handled++; return nil })
			if !errors.Is(err, context.Canceled) || creates != 2 || handled != 2 || commits != 2 {
				t.Fatalf("err=%v creates=%d handled=%d commits=%d", err, creates, handled, commits)
			}
			for _, client := range clients {
				if client.closeCount() != 1 || client.allowRebalanceCount() == 0 {
					t.Fatalf("closes=%d allows=%d", client.closeCount(), client.allowRebalanceCount())
				}
			}
		})
	}
}

func TestConsumerDoesNotRecoverHandlerNetworkFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	creates, commits := 0, 0
	client := &recoveryClient{consumerClientStub: &consumerClientStub{}}
	client.poll = func(context.Context, int) kgo.Fetches { return testFetches(testRecord("orders", 0, 1)) }
	client.commit = func(context.Context, ...*kgo.Record) error { commits++; return nil }
	value := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
		creates++
		return client, nil
	})
	err := value.Consume(ctx, func(context.Context, Delivery) error { return io.EOF })
	if !errors.Is(err, io.EOF) || creates != 1 || commits != 0 || client.closeCount() != 1 {
		t.Fatalf("err=%v creates=%d commits=%d closes=%d", err, creates, commits, client.closeCount())
	}
}

func TestConsumerDoesNotRecoverFatalKafkaErrors(t *testing.T) {
	for _, failure := range []error{kerr.TopicAuthorizationFailed, kerr.SaslAuthenticationFailed, kgo.ErrClientClosed} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			creates := 0
			client := &consumerClientStub{poll: func(context.Context, int) kgo.Fetches { return kgo.NewErrFetch(failure) }}
			value := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
				creates++
				return client, nil
			})
			err := value.Consume(ctx, func(context.Context, Delivery) error { return nil })
			if !errors.Is(err, failure) || creates != 1 || client.closeCount() != 1 {
				t.Fatalf("err=%v creates=%d closes=%d", err, creates, client.closeCount())
			}
		})
	}
}

func TestConsumerBoundsAmbiguousFirstReadRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		recover     bool
		joinedFatal bool
		wantCreates int
	}{
		{"restart recovers", true, false, 2}, {"persistent protocol mismatch", false, false, 4}, {"authorization remains fatal", false, true, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failure := error(&kgo.ErrFirstReadEOF{})
				if scenario.joinedFatal {
					failure = errors.Join(failure, kerr.GroupAuthorizationFailed)
				}
				creates := 0
				value := newConsumer(ConsumerConfig{Concurrency: 1}, nil, func(context.Context, string, string, ...kgo.Opt) (consumerClient, error) {
					creates++
					if scenario.recover && creates == 2 {
						cancel()
					}
					return &consumerClientStub{poll: func(ctx context.Context, _ int) kgo.Fetches {
						if ctx.Err() != nil {
							return kgo.NewErrFetch(ctx.Err())
						}
						return kgo.NewErrFetch(&kgo.ErrGroupSession{Err: failure})
					}}, nil
				})
				err := value.Consume(ctx, func(context.Context, Delivery) error { t.Error("unexpected handler"); return nil })
				if creates != scenario.wantCreates {
					t.Fatalf("creates=%d want=%d err=%v", creates, scenario.wantCreates, err)
				}
				if scenario.recover {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestConsumerRecoveryBackoffGrowsAndResetsAfterCommit(t *testing.T) {
	logger, path := newQueueKafkaFileLogger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = foundationlog.WithKv(ctx, "request.id", "consumer-1")
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
	if err := value.Consume(ctx, func(context.Context, Delivery) error { return nil }); !errors.Is(err, terminal) {
		t.Fatalf("Consume error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var attempts []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Reconnecting the Kafka consumer after a connection failure") {
			attempts = append(attempts, line)
		}
	}
	if len(attempts) != 3 {
		t.Fatalf("recovery logs = %v", attempts)
	}
	for index, expected := range []string{"attempt=1", "attempt=2", "attempt=1"} {
		if !strings.Contains(attempts[index], expected) || !strings.Contains(attempts[index], "request.id=consumer-1") {
			t.Fatalf("log %d = %s, want %s and consumer context", index, attempts[index], expected)
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
	err := value.Consume(ctx, func(context.Context, Delivery) error { return nil })
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
	err := value.Consume(ctx, func(context.Context, Delivery) error { return nil })
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
	err := value.Consume(ctx, func(context.Context, Delivery) error { return nil })
	if !errors.Is(err, terminal) || creates != 2 || client.closeCount() != 1 {
		t.Fatalf("err=%v creates=%d closes=%d", err, creates, client.closeCount())
	}
}

type sessionBoundClient struct {
	*consumerClientStub
	ctx      context.Context
	t        *testing.T
	leaves   int
	leaveErr error
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
	return c.leaveErr
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
	if err := value.Consume(ctx, func(context.Context, Delivery) error { return nil }); !errors.Is(err, terminal) {
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
	logger, path := newQueueKafkaFileLogger(t)
	ctx, cancel := context.WithCancel(foundationlog.WithKv(context.Background(), "request.id", "shutdown-1"))
	defer cancel()
	var client *sessionBoundClient
	value := newConsumer(ConsumerConfig{Concurrency: 1}, logger,
		func(session context.Context, _, _ string, _ ...kgo.Opt) (consumerClient, error) {
			client = &sessionBoundClient{ctx: session, t: t, consumerClientStub: &consumerClientStub{
				poll: func(ctx context.Context, _ int) kgo.Fetches {
					cancel()
					return kgo.NewErrFetch(ctx.Err())
				},
			}, leaveErr: errors.New("leave failed")}
			return client, nil
		})
	if err := value.Consume(ctx, func(context.Context, Delivery) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Consume = %v", err)
	}
	if client.leaves != 1 || client.closeCount() != 1 {
		t.Fatalf("leaves=%d closes=%d", client.leaves, client.closeCount())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"Kafka consumer is ready", "Failed to leave the Kafka consumer group"} {
		var matched string
		for _, line := range strings.Split(string(written), "\n") {
			if strings.Contains(line, event) {
				matched = line
				break
			}
		}
		if !strings.Contains(matched, "request.id=shutdown-1") {
			t.Fatalf("%q log lost worker context: %s", event, matched)
		}
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
		err := value.Consume(ctx, func(context.Context, Delivery) error { return nil })
		if !errors.Is(err, context.Canceled) || client.leaves != 1 || client.closeCount() != 1 {
			t.Fatalf("Consume=%v leaves=%d closes=%d", err, client.leaves, client.closeCount())
		}
		if elapsed := time.Since(started); elapsed <= 0 || elapsed > 5*time.Second {
			t.Fatalf("shutdown elapsed=%s, expected a finite LeaveGroup wait", elapsed)
		}
	})
}

func TestConsumerRecoveryUsesEarliestOnlyForMissingCommittedOffsets(t *testing.T) {
	config := ConsumerConfig{Topic: "orders", Group: "billing", Instance: "billing-1", Concurrency: 1, StartPosition: StartLatest}
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
	if err := value.Consume(ctx, func(context.Context, Delivery) error { return nil }); !errors.Is(err, terminal) {
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
