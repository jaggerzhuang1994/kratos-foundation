package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

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
	}))
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
	}))
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
