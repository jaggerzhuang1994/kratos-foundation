package reconnect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	var backoff Backoff
	for attempt, bounds := range [][2]time.Duration{
		{80 * time.Millisecond, 100 * time.Millisecond},
		{160 * time.Millisecond, 200 * time.Millisecond},
		{320 * time.Millisecond, 400 * time.Millisecond},
		{640 * time.Millisecond, 800 * time.Millisecond},
		{1280 * time.Millisecond, 1600 * time.Millisecond},
		{2560 * time.Millisecond, 3200 * time.Millisecond},
		{4 * time.Second, 5 * time.Second},
	} {
		for range 30 {
			if delay := backoff.Delay(attempt); delay < bounds[0] || delay > bounds[1] {
				t.Fatalf("attempt %d delay %s outside %v", attempt, delay, bounds)
			}
		}
	}
	for _, attempt := range []int{64, 1 << 30} {
		if delay := backoff.Delay(attempt); delay < 4*time.Second || delay > 5*time.Second {
			t.Fatalf("large attempt delay = %s", delay)
		}
	}
	if delay := (Backoff{Min: time.Second, Max: time.Millisecond}).Delay(2); delay > time.Millisecond {
		t.Fatalf("delay exceeded explicit cap: %s", delay)
	}
}

func TestBackoffWaitIsCancelable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backoff := Backoff{Min: time.Second}
		start := time.Now()
		if err := backoff.Wait(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed < 1600*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("elapsed = %s", elapsed)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(time.Millisecond); cancel() }()
		start = time.Now()
		if err := backoff.Wait(ctx, 20); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait error = %v", err)
		}
		if elapsed := time.Since(start); elapsed != time.Millisecond {
			t.Fatalf("cancellation delayed by %s", elapsed)
		}
	})
}

func TestTransientOnlyAcceptsConnectionFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false}, {io.EOF, true}, {io.ErrUnexpectedEOF, true},
		{context.Canceled, false}, {net.ErrClosed, false},
		{context.DeadlineExceeded, true},
		{syscall.ECONNREFUSED, true}, {syscall.ECONNRESET, true},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.EACCES}, false},
		{&net.DNSError{IsNotFound: true}, false},
		{&net.DNSError{IsTimeout: true}, true},
		{&net.AddrError{Err: "invalid port"}, false},
		{net.UnknownNetworkError("invalid"), false},
		{errors.New("authentication failed"), false},
	} {
		if got := Transient(tc.err); got != tc.want {
			t.Errorf("Transient(%v) = %v, want %v", tc.err, got, tc.want)
		}
		if tc.err != nil && Transient(fmt.Errorf("connect: %w", tc.err)) != tc.want {
			t.Errorf("wrapped error misclassified: %v", tc.err)
		}
	}
}
