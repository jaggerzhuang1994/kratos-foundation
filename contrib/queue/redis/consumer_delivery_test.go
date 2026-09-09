package redis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestProcessDeliveryAcknowledgesOnlyAfterHandlerAndHeartbeatSucceed(t *testing.T) {
	acknowledged := false
	delivery := queue.Delivery{Message: &queue.Message{ID: "message-1"}}
	err := processDelivery(
		context.Background(),
		delivery,
		func(_ context.Context, got queue.Delivery) error {
			if got.Message != delivery.Message || got.Err != delivery.Err {
				t.Fatalf("delivery = %#v, want %#v", got, delivery)
			}
			return nil
		},
		func() error { return nil },
		func() error { acknowledged = true; return nil },
	)
	if err != nil || !acknowledged {
		t.Fatalf("error=%v acknowledged=%v", err, acknowledged)
	}
	handlerErr := errors.New("handler failed")
	acknowledged = false
	err = processDelivery(
		context.Background(),
		delivery,
		func(context.Context, queue.Delivery) error { return handlerErr },
		func() error { return nil },
		func() error { acknowledged = true; return nil },
	)
	if !errors.Is(err, handlerErr) || acknowledged {
		t.Fatalf("error=%v acknowledged=%v", err, acknowledged)
	}
}

func TestProcessDeliveryPreservesProtocolOrderAndFailureChains(t *testing.T) {
	delivery := queue.Delivery{Message: &queue.Message{ID: "message-1"}}
	handlerErr := errors.New("handler failed")
	heartbeatErr := errors.New("heartbeat failed")
	ackErr := errors.New("ack failed")
	tests := []struct {
		name      string
		ctx       func() (context.Context, context.CancelFunc)
		handler   func(context.Context, queue.Delivery) error
		cleanup   func() error
		ack       func() error
		wantErr   error
		wantOrder []string
	}{
		{
			name: "handler",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			handler: func(context.Context, queue.Delivery) error {
				return handlerErr
			},
			cleanup:   func() error { return nil },
			ack:       func() error { return nil },
			wantErr:   handlerErr,
			wantOrder: []string{"handler", "cleanup"},
		},
		{
			name: "heartbeat cleanup",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			handler: func(context.Context, queue.Delivery) error {
				return nil
			},
			cleanup:   func() error { return heartbeatErr },
			ack:       func() error { return nil },
			wantErr:   heartbeatErr,
			wantOrder: []string{"handler", "cleanup"},
		},
		{
			name: "context cancellation",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			handler: func(ctx context.Context, _ queue.Delivery) error {
				ctx.Value(cancelContextKey{}).(context.CancelFunc)()
				return nil
			},
			cleanup:   func() error { return nil },
			ack:       func() error { return nil },
			wantErr:   context.Canceled,
			wantOrder: []string{"handler", "cleanup"},
		},
		{
			name: "XACK",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			handler: func(context.Context, queue.Delivery) error {
				return nil
			},
			cleanup:   func() error { return nil },
			ack:       func() error { return ackErr },
			wantErr:   ackErr,
			wantOrder: []string{"handler", "cleanup", "ack"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.ctx()
			defer cancel()
			ctx = context.WithValue(ctx, cancelContextKey{}, context.CancelFunc(cancel))
			var order []string
			err := processDelivery(
				ctx,
				delivery,
				func(ctx context.Context, delivery queue.Delivery) error {
					order = append(order, "handler")
					return test.handler(ctx, delivery)
				},
				func() error {
					order = append(order, "cleanup")
					return test.cleanup()
				},
				func() error {
					order = append(order, "ack")
					return test.ack()
				},
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if len(order) != len(test.wantOrder) {
				t.Fatalf("order=%v, want %v", order, test.wantOrder)
			}
			for index := range order {
				if order[index] != test.wantOrder[index] {
					t.Fatalf("order=%v, want %v", order, test.wantOrder)
				}
			}
		})
	}
}

func TestProcessDeliveryJoinsHandlerAndHeartbeatErrorsWithoutAck(t *testing.T) {
	handlerErr := errors.New("handler failed")
	heartbeatErr := errors.New("heartbeat failed")
	acknowledged := false
	err := processDelivery(
		context.Background(),
		queue.Delivery{Message: &queue.Message{ID: "message-1"}},
		func(context.Context, queue.Delivery) error { return handlerErr },
		func() error { return heartbeatErr },
		func() error { acknowledged = true; return nil },
	)
	if !errors.Is(err, handlerErr) || !errors.Is(err, heartbeatErr) || acknowledged {
		t.Fatalf("error=%v acknowledged=%v", err, acknowledged)
	}
}

func TestInFlightHeartbeatClaimErrorSurvivesCleanupAndPreventsAck(t *testing.T) {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	claimStarted := make(chan struct{})
	redisErr := errors.New("redis connection failed")
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- runHeartbeat(
			heartbeatCtx,
			ticks,
			"message-1",
			func(ctx context.Context) ([]string, error) {
				close(claimStarted)
				<-ctx.Done()
				return nil, redisErr
			},
		)
	}()

	acknowledged := false
	err := processDelivery(
		context.Background(),
		queue.Delivery{Message: &queue.Message{ID: "message-1"}},
		func(context.Context, queue.Delivery) error {
			ticks <- time.Now()
			<-claimStarted
			return nil
		},
		func() error {
			cancelHeartbeat()
			return <-heartbeatDone
		},
		func() error { acknowledged = true; return nil },
	)
	if !errors.Is(err, redisErr) || acknowledged {
		t.Fatalf("error=%v acknowledged=%v", err, acknowledged)
	}
}

func TestInFlightHeartbeatWrappedCancellationAllowsAck(t *testing.T) {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	claimStarted := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- runHeartbeat(
			heartbeatCtx,
			ticks,
			"message-1",
			func(ctx context.Context) ([]string, error) {
				close(claimStarted)
				<-ctx.Done()
				return nil, fmt.Errorf("claim interrupted: %w", ctx.Err())
			},
		)
	}()

	acknowledged := false
	err := processDelivery(
		context.Background(),
		queue.Delivery{Message: &queue.Message{ID: "message-1"}},
		func(context.Context, queue.Delivery) error {
			ticks <- time.Now()
			<-claimStarted
			return nil
		},
		func() error {
			cancelHeartbeat()
			return <-heartbeatDone
		},
		func() error { acknowledged = true; return nil },
	)
	if err != nil || !acknowledged {
		t.Fatalf("error=%v acknowledged=%v", err, acknowledged)
	}
}
