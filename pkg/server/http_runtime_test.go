package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
)

func TestHTTPRuntimeEndpointFailureClosesCallerListener(t *testing.T) {
	listener := newHTTPRuntimeCountingListener(t)
	address := listener.Listener.Addr().String()
	// 底层保留真实 TCP socket，非 TCP Addr 只用于触发 SDK 端口提取失败。
	listener.address = &net.UnixAddr{Name: "endpoint-test", Net: "unix"}
	spec := NewSpec()
	spec.HTTP().Listener(listener)
	runtime, cleanup := newHTTPRuntimeFixture(t, spec, "127.0.0.1:0")
	if _, err := runtime.http.Endpoint(); err == nil {
		t.Fatal("Endpoint accepted a listener without TCP port metadata")
	}
	// 失败入口须自行回收监听，不能依赖后续 AbortStartup 或 cleanup。
	if got := listener.closes.Load(); got != 1 {
		t.Fatalf("caller listener Close count after Endpoint = %d, want 1", got)
	}
	assertHTTPRuntimeAddressReleased(t, address)
	if err := runtime.http.AbortStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()
	if got := listener.closes.Load(); got != 1 {
		t.Fatalf("caller listener Close count = %d, want 1", got)
	}
	assertHTTPRuntimeAddressReleased(t, address)
}

func TestHTTPRuntimeTLSStartFailureClosesCallerListener(t *testing.T) {
	closeFailure := errors.New("listener close failed")
	for _, tt := range []struct {
		name     string
		closeErr error
	}{{"closed", nil}, {"close failure preserved", closeFailure}} {
		t.Run(tt.name, func(t *testing.T) {
			listener := newHTTPRuntimeCountingListener(t)
			listener.closeErr = tt.closeErr
			address := listener.Listener.Addr().String()
			spec := NewSpec()
			// 空 TLS 配置会在 ServeTLS 加载证书阶段失败，尚未进入 Serve 管理监听。
			spec.HTTP().Listener(listener).Option(kratoshttp.TLSConfig(&tls.Config{}))
			runtime, cleanup := newHTTPRuntimeFixture(t, spec, "127.0.0.1:0")
			ctx, cancel := context.WithTimeout(t.Context(), runtimeTestTimeout)
			defer cancel()
			err := runtime.http.Start(ctx)
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || ctx.Err() != nil {
				t.Fatalf("Start error = %v, context error = %v; want certificate load failure", err, ctx.Err())
			}
			if tt.closeErr != nil && !errors.Is(err, tt.closeErr) {
				t.Fatalf("Start error = %v, want joined listener close failure", err)
			}
			// ServeTLS 尚未接管 socket 时，Start 失败也须立即释放资源。
			if got := listener.closes.Load(); got != 1 {
				t.Fatalf("caller listener Close count after Start = %d, want 1", got)
			}
			assertHTTPRuntimeAddressReleased(t, address)
			cleanup()
			cleanup()
			if got := listener.closes.Load(); got != 1 {
				t.Fatalf("caller listener Close count = %d, want 1", got)
			}
			assertHTTPRuntimeAddressReleased(t, address)
		})
	}
}

func TestHTTPRuntimePrestartReleaseIsIdempotent(t *testing.T) {
	for _, action := range []string{"cleanup", "abort", "stop", "canceled start"} {
		t.Run(action, func(t *testing.T) {
			listener := newHTTPRuntimeCountingListener(t)
			address := listener.Listener.Addr().String()
			spec := NewSpec()
			spec.HTTP().Listener(listener)
			runtime, cleanup := newHTTPRuntimeFixture(t, spec, "127.0.0.1:0")
			if action != "canceled start" {
				if _, err := runtime.http.Endpoint(); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := range 2 {
				var err error
				switch action {
				case "cleanup":
					cleanup()
				case "abort":
					err = runtime.http.AbortStartup(context.Background())
				case "stop":
					err = runtime.http.Stop(context.Background())
				case "canceled start":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					err = runtime.http.Start(ctx)
				}
				if action == "canceled start" {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("Start error = %v, want canceled", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					// 每个释放入口返回时就应关闭资源，不能让后续 cleanup 掩盖泄漏。
					if got := listener.closes.Load(); got != 1 {
						t.Fatalf("caller listener Close count after %s = %d, want 1", action, got)
					}
					assertHTTPRuntimeAddressReleased(t, address)
				}
			}
			cleanup()
			if got := listener.closes.Load(); got != 1 {
				t.Fatalf("caller listener Close count = %d, want 1", got)
			}
			assertHTTPRuntimeAddressReleased(t, address)
		})
	}
}

func TestHTTPRuntimePreservesTLSEndpointOptions(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "tls scheme"
		if custom {
			name = "custom endpoint"
		}
		t.Run(name, func(t *testing.T) {
			listener := newHTTPRuntimeCountingListener(t)
			tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
			spec := NewSpec()
			spec.HTTP().Listener(listener).Option(kratoshttp.TLSConfig(tlsConfig))
			advertised := &url.URL{Scheme: "https", Host: "advertised.example:443", Path: "/api", RawQuery: "cluster=test"}
			if custom {
				spec.HTTP().Option(kratoshttp.Endpoint(advertised))
			}
			runtime, _ := newHTTPRuntimeFixture(t, spec, "127.0.0.1:0")
			endpoint, err := runtime.http.Endpoint()
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Scheme != "https" || runtime.http.TLSConfig != tlsConfig {
				t.Fatalf("native TLS option lost: endpoint=%s config=%p", endpoint, runtime.http.TLSConfig)
			}
			if custom && endpoint.String() != advertised.String() {
				t.Fatalf("Endpoint = %s, want %s", endpoint, advertised)
			}
			original := endpoint.String()
			endpoint.Host = "modified.example:8443"
			endpoint.Path = "/modified"
			again, err := runtime.http.Endpoint()
			if err != nil || again.String() != original {
				t.Fatalf("Endpoint is not an independent URL: %v %v", again, err)
			}
		})
	}
}

func TestHTTPRuntimeRegistrationDoesNotBindListener(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	address := occupied.Addr().String()
	var nativeErr error
	spec := NewSpec()
	spec.HTTP().Register(func(server HTTPServer) error {
		// 配置回调禁止生命周期操作；即使误调用原生 Endpoint，也不能隐式绑定 socket。
		_, nativeErr = server.Endpoint()
		return nil
	})
	runtime, _ := newHTTPRuntimeFixture(t, spec, address)
	if nativeErr == nil || errors.Is(nativeErr, syscall.EADDRINUSE) {
		t.Fatalf("native Endpoint during registration = %v, want unprepared listener error", nativeErr)
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	// SDK 缓存配置回调的错误；后续准备失败仍须回收新分配的监听，不能泄漏。
	if _, err := runtime.http.Endpoint(); !errors.Is(err, nativeErr) {
		t.Fatalf("Endpoint error = %v, want original cached SDK error", err)
	}
	assertHTTPRuntimeAddressReleased(t, address)
}

func TestHTTPRuntimeConfiguredListenerOverridesNativeOptions(t *testing.T) {
	ignored := newHTTPRuntimeCountingListener(t)
	nativeAddress := ignored.Listener.Addr().String()
	server := newManagedHTTPServer("tcp4", "127.0.0.1:0", nil,
		kratoshttp.Network("unix"), kratoshttp.Address("ignored-native-socket"), kratoshttp.Listener(ignored))
	t.Cleanup(func() {
		if err := server.AbortStartup(context.Background()); err != nil {
			t.Error(err)
		}
	})
	endpoint, err := server.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(endpoint.Host, "127.0.0.1:") || endpoint.Host == nativeAddress {
		t.Fatalf("Endpoint = %s, want independent listener at configured TCP address", endpoint)
	}
	if err := server.AbortStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertHTTPRuntimeAddressReleased(t, endpoint.Host)
	if got := ignored.closes.Load(); got != 0 {
		t.Fatalf("ignored native listener Close count = %d, want retained caller ownership", got)
	}
	if err := ignored.Close(); err != nil {
		t.Fatal(err)
	}
	assertHTTPRuntimeAddressReleased(t, nativeAddress)
}

func TestHTTPRuntimeDisabledListenerRetainsCallerOwnership(t *testing.T) {
	listener := newHTTPRuntimeCountingListener(t)
	address := listener.Listener.Addr().String()
	spec := NewSpec()
	spec.HTTP().Listener(listener)
	manager := newRuntimeShutdownManager(t, &config_pb.Server{
		Http: &config_pb.HttpServerOption{Disable: proto.Bool(true)},
		Grpc: &config_pb.GrpcServerOption{Disable: proto.Bool(true)},
	}, runtimeTestTimeout)
	runtime, cleanup, err := NewRuntime(manager, newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	cleanup()
	cleanup()
	if runtime.http != nil || listener.closes.Load() != 0 {
		t.Fatalf("disabled HTTP took listener ownership: runtime=%v closes=%d", runtime.http, listener.closes.Load())
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	assertHTTPRuntimeAddressReleased(t, address)
}

type httpRuntimeCountingListener struct {
	net.Listener
	address  net.Addr
	closeErr error
	closes   atomic.Int32
}

func (l *httpRuntimeCountingListener) Addr() net.Addr {
	if l.address != nil {
		return l.address
	}
	return l.Listener.Addr()
}

func (l *httpRuntimeCountingListener) Close() error {
	l.closes.Add(1)
	return errors.Join(l.Listener.Close(), l.closeErr)
}

func newHTTPRuntimeCountingListener(t *testing.T) *httpRuntimeCountingListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// 紧急测试清理直接关闭底层，不计入运行时拥有者的 Close 次数。
	t.Cleanup(func() { _ = listener.Close() })
	return &httpRuntimeCountingListener{Listener: listener}
}

func newHTTPRuntimeFixture(t *testing.T, spec *Spec, address string) (*Runtime, func()) {
	t.Helper()
	manager := newRuntimeShutdownManager(t, &config_pb.Server{
		Http: &config_pb.HttpServerOption{Addr: proto.String(address)},
		Grpc: &config_pb.GrpcServerOption{Disable: proto.Bool(true)},
	}, runtimeTestTimeout)
	runtime, cleanup, err := NewRuntime(manager, newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return runtime, cleanup
}

func assertHTTPRuntimeAddressReleased(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP address remains bound: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
