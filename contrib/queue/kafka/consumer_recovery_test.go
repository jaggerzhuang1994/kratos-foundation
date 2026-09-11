package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

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
			if got := transientKafkaError(test.err, false); got != test.want {
				t.Fatalf("transientKafkaError(%v)=%v", test.err, got)
			}
		})
	}
}
