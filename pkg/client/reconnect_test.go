package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestHTTPTransportReusesConcurrentWaves(t *testing.T) {
	for _, width := range []int{1, 8, 32} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			arrived := make(chan struct{}, width)
			gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
			var connections atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				arrived <- struct{}{}
				wave := 0
				if r.URL.Path == "/1" {
					wave = 1
				}
				select {
				case <-gates[wave]:
				case <-ctx.Done():
				}
				_, _ = io.WriteString(w, "ok")
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			transport, _, cleanup := newHTTPClientTransport(ctx, false)
			defer cleanup()
			client := &http.Client{Transport: transport}
			first := int64(0)
			for wave := 0; wave < 2; wave++ {
				done := make(chan error, width)
				for range width {
					go func() {
						req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%d", server.URL, wave), nil)
						if err != nil {
							done <- err
							return
						}
						res, err := client.Do(req)
						if err == nil {
							_, err = io.Copy(io.Discard, res.Body)
							closeErr := res.Body.Close()
							if err == nil {
								err = closeErr
							}
						}
						done <- err
					}()
				}
				for range width {
					select {
					case <-arrived:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				close(gates[wave])
				for range width {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				if wave == 0 {
					first = connections.Load()
				}
			}
			extra := connections.Load() - first
			t.Logf("width=%d first=%d second_new=%d", width, first, extra)
			if extra != 0 {
				t.Fatalf("second wave created %d unnecessary connections", extra)
			}
		})
	}
}

func TestHTTPTransportPreservesExplicitIdleLimits(t *testing.T) {
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	base := previous.(*http.Transport).Clone()
	base.MaxIdleConnsPerHost = 7
	base.MaxIdleConns = 11
	base.MaxConnsPerHost = 13
	http.DefaultTransport = base
	rt, _, cleanup := newHTTPClientTransport(context.Background(), false)
	defer cleanup()
	got := rt.(*http.Transport)
	if got.MaxIdleConnsPerHost != 7 || got.MaxIdleConns != 11 || got.MaxConnsPerHost != 13 {
		t.Fatal("explicit limits changed")
	}
	if base.MaxIdleConnsPerHost != 7 {
		t.Fatal("source transport modified")
	}
}
