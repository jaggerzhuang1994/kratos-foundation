package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestLifetimePreservesWritableBody(t *testing.T) {
	for _, writable := range []bool{false, true} {
		t.Run(map[bool]string{false: "read only", true: "writable"}[writable], func(t *testing.T) {
			var output strings.Builder
			body := io.NopCloser(strings.NewReader("response"))
			if writable {
				body = struct {
					io.ReadCloser
					io.Writer
				}{body, &output}
			}
			transport := withRequestLifetime(context.Background(), roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: body}, nil
			}))
			request, err := http.NewRequest(http.MethodGet, "http://example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			writer, ok := response.Body.(io.Writer)
			if ok != writable {
				t.Fatalf("body writable = %v, want %v", ok, writable)
			}
			if writable {
				if _, err := writer.Write([]byte("request")); err != nil {
					t.Fatal(err)
				}
				if output.String() != "request" {
					t.Fatalf("write not forwarded: %q", output.String())
				}
			}
			if got, err := io.ReadAll(response.Body); err != nil || string(got) != "response" {
				t.Fatalf("read = %q, %v", got, err)
			}
		})
	}
}

// 验证真实 HTTP 网络调用，避免只断言 Close 被调用却没有中断在途请求。
func TestClosingBuiltHTTPClientCancelsResponseBody(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := newTestRealBuilder(t, nil).build(context.Background(), newClientSpec("orders", configWithTarget("orders", server.URL).Clients["orders"]))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := result.httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-started
	if err := result.close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(response.Body); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("body read survived client close")
		}
	default:
		// 新行为应最终取消请求；使用有界等待并在失败时清理测试请求。
		select {
		case err := <-done:
			if err == nil {
				t.Error("body read completed without cancellation")
			}
		case <-time.After(time.Second):
			cancel()
			<-done
			t.Error("client close did not cancel in-flight HTTP body")
		}
	}
}
