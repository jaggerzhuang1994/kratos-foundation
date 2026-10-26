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

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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

// 旧连接断开后，第二个 POST 在建连阶段恢复，业务请求只发送一次。
func TestHTTPClientReconnectsWithoutReplayingRequest(t *testing.T) {
	var requests, dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		attempt := dials.Add(1)
		if attempt == 2 || attempt == 3 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	protocol := config_pb.Protocol_HTTP
	result, err := newTestRealBuilder(t, nil).build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol, Target: server.URL,
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	for request := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		resp, err := result.httpClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("request %d: %v", request, err)
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		cancel()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read/close: %v / %v", readErr, closeErr)
		}
		server.CloseClientConnections()
	}
	if requests.Load() != 2 || dials.Load() != 4 {
		t.Fatalf("requests=%d dials=%d, want 2 requests over 4 dial attempts", requests.Load(), dials.Load())
	}
}

func TestHTTPDoesNotReplayAfterServerReceivesRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	protocol := config_pb.Protocol_HTTP
	result, err := newTestRealBuilder(t, nil).build(context.Background(), newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol, Target: server.URL,
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := result.httpClient.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, io.EOF) || requests.Load() != 1 {
		t.Fatalf("error=%v requests=%d, want EOF after one request", err, requests.Load())
	}
}

func TestGRPCClientSurvivesServerRestart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	start := func(listener net.Listener) *grpc.Server {
		server := grpc.NewServer()
		healthpb.RegisterHealthServer(server, health.NewServer())
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		return server
	}
	first := start(listener)
	result, err := newTestRealBuilder(t, nil).build(context.Background(), newClientSpec("health", &config_pb.ClientOption{
		Target: "passthrough:///" + address,
	}, nil, observability.Defaults{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	client := healthpb.NewHealthClient(result.grpcClient)
	check := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
		if err != nil {
			t.Fatal(err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health = %v", response)
		}
	}
	check()
	first.Stop()
	offlineCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = client.Check(offlineCtx, &healthpb.HealthCheckRequest{})
	cancel()
	if err == nil {
		t.Fatal("RPC unexpectedly succeeded while server was stopped")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	start(listener)
	// 同一个已借出的 ClientConn 恢复；调用方不重建 Factory 或业务客户端。
	check()
}
