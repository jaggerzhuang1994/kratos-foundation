package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestHTTPDialRetryBoundsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		failure          error
		timeout          time.Duration
		wantCalls        int
		minWait, maxWait time.Duration
	}{
		{"network exhausted", io.EOF, time.Minute, 5, 1200 * time.Millisecond, 1500 * time.Millisecond},
		{"invalid address", &net.AddrError{Err: "invalid"}, time.Minute, 1, 0, 0},
		{"cancel during wait", io.EOF, 50 * time.Millisecond, 1, 50 * time.Millisecond, 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
				defer cancel()
				calls := 0
				started := time.Now()
				_, err := dialHTTPConnection(ctx, func(context.Context, string, string) (net.Conn, error) {
					calls++
					return nil, tc.failure
				}, "tcp", "orders:80")
				if calls != tc.wantCalls || err == nil {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if elapsed := time.Since(started); elapsed < tc.minWait || elapsed > tc.maxWait {
					t.Fatalf("wait=%s outside %s..%s", elapsed, tc.minWait, tc.maxWait)
				}
			})
		})
	}
}

func TestHTTPTransportCleanupCancelsPendingDial(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	started := make(chan struct{})
	base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	previous := http.DefaultTransport
	http.DefaultTransport = base
	t.Cleanup(func() { http.DefaultTransport = previous })
	transport, _, cleanup := newHTTPClientTransport(context.Background(), false)
	defer cleanup()
	done := make(chan error, 1)
	go func() {
		_, err := transport.(*http.Transport).DialContext(context.Background(), "tcp", "orders:80")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	cleanup()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dial error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup left dial running")
	}
}

// 验证配置实际作用于 gRPC 状态机，长断网不会退避到默认的两分钟量级。
func TestGRPCReconnectBackoffStaysBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts []time.Time
		conn, err := grpc.NewClient("passthrough:///unavailable",
			grpc.WithTransportCredentials(insecure.NewCredentials()), grpcReconnectOption(),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				attempts = append(attempts, time.Now())
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		conn.Connect()
		time.Sleep(40 * time.Second)
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(attempts) < 8 {
			t.Fatalf("only %d reconnect attempts over 40s", len(attempts))
		}
		for i := 1; i < len(attempts); i++ {
			if gap := attempts[i].Sub(attempts[i-1]); gap > 6*time.Second {
				t.Fatalf("reconnect gap=%s exceeds 5s base with 20%% jitter", gap)
			}
		}
	})
}
