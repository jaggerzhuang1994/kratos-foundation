package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
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
			err := value.Consume(ctx, func(context.Context, queue.Delivery) error { handled++; return nil })
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
	err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return io.EOF })
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
			err := value.Consume(ctx, func(context.Context, queue.Delivery) error { return nil })
			if !errors.Is(err, failure) || creates != 1 || client.closeCount() != 1 {
				t.Fatalf("err=%v creates=%d closes=%d", err, creates, client.closeCount())
			}
		})
	}
}

func TestTransientKafkaErrorPreservesProtocolAndJoinedFailureBoundaries(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"eof", io.EOF, true},
		{"dial refused", &net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}, true},
		{"request timed out", kerr.RequestTimedOut, true},
		{"epoch", kerr.FencedMemberEpoch, true},
		{"closed", kgo.ErrClientClosed, false},
		{"canceled", context.Canceled, false},
		{"first read", &kgo.ErrFirstReadEOF{}, false},
		{"authentication", kerr.SaslAuthenticationFailed, false},
		{"authorization", kerr.GroupAuthorizationFailed, false},
		{"joined transient", errors.Join(io.EOF, kerr.RequestTimedOut), true},
		{"joined fatal", errors.Join(io.EOF, kerr.TopicAuthorizationFailed), false},
		{"wrapped joined fatal", fmt.Errorf("request: %w", fmt.Errorf("broker: %w", errors.Join(io.EOF, kerr.TopicAuthorizationFailed))), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transientKafkaError(test.err); got != test.want {
				t.Fatalf("transientKafkaError(%v)=%v", test.err, got)
			}
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
