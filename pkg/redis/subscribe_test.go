package redis

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const subscribeTestTimeout = time.Second

func receiveWithin[T any](t testing.TB, channel <-chan T, operation string) (T, bool) {
	t.Helper()
	select {
	case value, ok := <-channel:
		return value, ok
	case <-time.After(subscribeTestTimeout):
		t.Fatalf("timed out waiting for %s", operation)
		var zero T
		return zero, false
	}
}

type nilSubscriptionClient struct{}

func (*nilSubscriptionClient) Subscribe(context.Context, ...string) *goredis.PubSub {
	return nil
}

type fakeMessageSubscription struct {
	messages       chan *goredis.Message
	receiveErr     error
	receiveStarted chan struct{}
	receiveRelease <-chan struct{}
	receiveOnce    sync.Once
	closeErr       error
	closed         chan struct{}
	closeOnce      sync.Once
}

func newFakeMessageSubscription(buffer int) *fakeMessageSubscription {
	return &fakeMessageSubscription{
		messages:       make(chan *goredis.Message, buffer),
		receiveStarted: make(chan struct{}),
		closed:         make(chan struct{}),
	}
}

func (s *fakeMessageSubscription) Receive(ctx context.Context) (any, error) {
	s.receiveOnce.Do(func() { close(s.receiveStarted) })
	if s.receiveRelease != nil {
		select {
		case <-s.receiveRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, s.receiveErr
}

func (s *fakeMessageSubscription) Channel(...goredis.ChannelOption) <-chan *goredis.Message {
	return s.messages
}

func (s *fakeMessageSubscription) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return s.closeErr
}

func TestSubscribeRejectsInvalidArgumentsBeforeOpeningStream(t *testing.T) {
	parser := func(message *goredis.Message) (string, error) { return message.Payload, nil }
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		client  Subscriber
		channel string
		parser  func(*goredis.Message) (string, error)
		options []SubscribeOption
		want    string
	}{
		{
			name:    "canceled context",
			ctx:     canceledCtx,
			client:  new(nilSubscriptionClient),
			channel: "events",
			parser:  parser,
			want:    context.Canceled.Error(),
		},
		{
			name:    "nil client",
			ctx:     context.Background(),
			channel: "events",
			parser:  parser,
			want:    "client is nil",
		},
		{
			name:    "blank channel",
			ctx:     context.Background(),
			client:  new(nilSubscriptionClient),
			channel: " \t",
			parser:  parser,
			want:    "channel is required",
		},
		{
			name:    "nil parser",
			ctx:     context.Background(),
			client:  new(nilSubscriptionClient),
			channel: "events",
			want:    "parser is nil",
		},
		{
			name:    "negative buffer",
			ctx:     context.Background(),
			client:  new(nilSubscriptionClient),
			channel: "events",
			parser:  parser,
			options: []SubscribeOption{WithSubscribeBufferSize(-1)},
			want:    "cannot be negative",
		},
		{
			name:    "nil subscription",
			ctx:     context.Background(),
			client:  new(nilSubscriptionClient),
			channel: "events",
			parser:  parser,
			want:    "nil subscription",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, err := Subscribe(
				test.ctx,
				test.client,
				test.channel,
				test.parser,
				test.options...,
			)
			if events != nil {
				t.Fatal("Subscribe returned an event stream for invalid arguments")
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Subscribe error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestStartSubscriptionWaitsForConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	release := make(chan struct{})
	subscription := newFakeMessageSubscription(1)
	subscription.receiveRelease = release
	type result struct {
		events <-chan SubscribeEvent[string]
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		events, err := startSubscription(
			ctx,
			subscription,
			func(message *goredis.Message) (string, error) { return message.Payload, nil },
			1,
		)
		resultCh <- result{events: events, err: err}
	}()

	receiveWithin(t, subscription.receiveStarted, "subscription confirmation to start")
	select {
	case <-resultCh:
		t.Fatal("startSubscription returned before Redis confirmation")
	default:
	}
	close(release)

	opened, ok := receiveWithin(t, resultCh, "confirmed subscription result")
	if !ok {
		t.Fatal("subscription result channel closed without a result")
	}
	if opened.err != nil || opened.events == nil {
		t.Fatalf("startSubscription = (%v, %v), want event stream", opened.events, opened.err)
	}
	cancel()
	if _, ok := receiveWithin(t, opened.events, "event stream to close after cancellation"); ok {
		t.Fatal("event stream emitted an unexpected event after cancellation")
	}
	select {
	case <-subscription.closed:
	case <-time.After(subscribeTestTimeout):
		t.Fatal("timed out waiting for confirmed subscription to close")
	}
}

func TestStartSubscriptionJoinsConfirmationAndCloseErrors(t *testing.T) {
	receiveErr := errors.New("confirmation failed")
	closeErr := errors.New("close failed")
	subscription := newFakeMessageSubscription(1)
	subscription.receiveErr = receiveErr
	subscription.closeErr = closeErr

	events, err := startSubscription(
		context.Background(),
		subscription,
		func(message *goredis.Message) (string, error) { return message.Payload, nil },
		1,
	)
	if events != nil {
		t.Fatal("failed confirmation returned an event stream")
	}
	if !errors.Is(err, receiveErr) || !errors.Is(err, closeErr) {
		t.Fatalf("startSubscription error = %v, want both errors", err)
	}
}

func TestConsumeSubscriptionPreservesMessageAndErrorOrder(t *testing.T) {
	parseErr := errors.New("invalid message")
	closeErr := errors.New("close failed")
	subscription := newFakeMessageSubscription(5)
	subscription.closeErr = closeErr
	events := make(chan SubscribeEvent[string], 6)

	go consumeSubscription(
		context.Background(),
		subscription,
		func(message *goredis.Message) (string, error) {
			switch message.Payload {
			case "bad":
				return "", parseErr
			case "panic":
				panic("parser failed")
			default:
				return message.Payload, nil
			}
		},
		events,
	)

	subscription.messages <- &goredis.Message{Payload: "first"}
	subscription.messages <- nil
	subscription.messages <- &goredis.Message{Payload: "bad"}
	subscription.messages <- &goredis.Message{Payload: "panic"}
	subscription.messages <- &goredis.Message{Payload: "last"}
	close(subscription.messages)

	received := make([]SubscribeEvent[string], 0, 6)
	for range 6 {
		event, ok := receiveWithin(t, events, "ordered subscription event")
		if !ok {
			t.Fatalf("event stream closed after %d events, want 6", len(received))
		}
		received = append(received, event)
	}
	if _, ok := receiveWithin(t, events, "ordered event stream to close"); ok {
		t.Fatal("event stream produced more than 6 events")
	}
	if received[0].Message != "first" || received[0].Err != nil {
		t.Fatalf("first event = %+v", received[0])
	}
	if received[1].Err == nil || !strings.Contains(received[1].Err.Error(), "nil message") {
		t.Fatalf("nil-message event = %+v", received[1])
	}
	if !errors.Is(received[2].Err, parseErr) {
		t.Fatalf("parser-error event = %+v", received[2])
	}
	if received[3].Err == nil || !strings.Contains(received[3].Err.Error(), "parser failed") {
		t.Fatalf("panic event = %+v", received[3])
	}
	if received[4].Message != "last" || received[4].Err != nil {
		t.Fatalf("last message event = %+v", received[4])
	}
	if !errors.Is(received[5].Err, closeErr) {
		t.Fatalf("close event = %+v", received[5])
	}
}

func TestConsumeSubscriptionCancellationUnblocksSendAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	subscription := newFakeMessageSubscription(1)
	events := make(chan SubscribeEvent[string])
	go consumeSubscription(
		ctx,
		subscription,
		func(message *goredis.Message) (string, error) { return message.Payload, nil },
		events,
	)

	subscription.messages <- &goredis.Message{Payload: "blocked"}
	cancel()

	select {
	case _, ok := <-events:
		if ok {
			select {
			case _, stillOpen := <-events:
				if stillOpen {
					t.Fatal("event stream remained open after cancellation")
				}
			case <-time.After(time.Second):
				t.Fatal("event stream did not close after cancellation")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock event delivery")
	}
	select {
	case <-subscription.closed:
	case <-time.After(time.Second):
		t.Fatal("subscription was not closed after cancellation")
	}
}

func TestConsumeSubscriptionReportsNilMessageStream(t *testing.T) {
	subscription := newFakeMessageSubscription(0)
	subscription.messages = nil
	events := make(chan SubscribeEvent[string], 1)
	go consumeSubscription(
		context.Background(),
		subscription,
		func(message *goredis.Message) (string, error) { return message.Payload, nil },
		events,
	)

	event, ok := receiveWithin(t, events, "nil-stream error event")
	if !ok || event.Err == nil || !strings.Contains(event.Err.Error(), "stream is nil") {
		t.Fatalf("nil-stream event = (%+v, %v)", event, ok)
	}
	if _, ok := receiveWithin(t, events, "nil event stream to close"); ok {
		t.Fatal("event stream remained open")
	}
}

// go-redis 的 Receive 没有默认读超时；普通 cancel 必须主动关闭尚未确认的订阅。
func TestSubscribeCancellationInterruptsConfirmation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	subscribed := make(chan net.Conn, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		for {
			command, err := readRESPReconnectCommand(reader)
			if err != nil {
				return
			}
			if command == "subscribe" {
				subscribed <- conn
				_, _ = io.Copy(io.Discard, conn)
				return
			}
			if _, err := io.WriteString(conn, "-ERR unknown command\r\n"); err != nil {
				return
			}
		}
	}()
	client := goredis.NewClient(&goredis.Options{
		Addr: listener.Addr().String(), Protocol: 2, DisableIdentity: true,
		ReadTimeout: 30 * time.Millisecond, ContextTimeoutEnabled: true, MaxRetries: -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := Subscribe(ctx, client, "orders", func(message *goredis.Message) (string, error) {
			return message.Payload, nil
		})
		done <- err
	}()
	conn, _ := receiveWithin(t, subscribed, "SUBSCRIBE command")
	t.Cleanup(func() { _ = conn.Close() })
	cancel()
	err, _ = receiveWithin(t, done, "confirmation cancellation")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe = %v, want context.Canceled", err)
	}
	receiveWithin(t, serverDone, "subscription socket closure")
}
