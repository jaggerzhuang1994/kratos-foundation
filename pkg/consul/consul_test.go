package consul

import (
	"context"
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type idleCloserStub struct {
	calls int
}

func (c *idleCloserStub) CloseIdleConnections() {
	c.calls++
}

func TestConsulCleanupIsIdempotent(t *testing.T) {
	closer := new(idleCloserStub)
	cleanup := newConsulCleanup(closer)
	cleanup()
	cleanup()
	if closer.calls != 1 {
		t.Fatalf("close calls = %d", closer.calls)
	}
}

func testLogger(t *testing.T) log.Logger {
	t.Helper()
	shared, cleanup, err := testlog.New(testlog.Config{TimeFormat: time.RFC3339, Std: testlog.OutputConfig{Disable: true}, File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
}

func TestNewSkipsWhenExplicitlyDisabled(t *testing.T) {
	t.Setenv(DisableConsul, "true")
	options := NewOptions()
	t.Setenv(DisableConsul, "false")
	t.Setenv("APP_ENV", "dev")
	client, cleanup, err := New(testLogger(t), options)
	if err != nil || client != nil {
		t.Fatalf("disabled New = %v, %v", client, err)
	}
	cleanup()
	cleanup()
}

func TestNewPreservesInputConfigAndExplicitEnableDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`"127.0.0.1:8300"`))
	}))
	defer server.Close()
	t.Setenv(DisableConsul, "false")
	t.Setenv("APP_ENV", "dev")
	t.Setenv(api.HTTPAddrEnvName, server.URL)
	options := NewOptions()
	if options.Disabled || options.Config.Address != server.URL {
		t.Fatalf("NewOptions = %#v, want enabled with configured address", options)
	}
	// 零值 SDK 字段由 NewClient 补全，不能反向改写调用方输入。
	options.Config = &api.Config{Address: server.URL}
	t.Setenv(DisableConsul, "true")
	t.Setenv("APP_ENV", "local")
	t.Setenv(api.HTTPAddrEnvName, "")
	client, cleanup, err := New(testLogger(t), options)
	defer cleanup()
	if err != nil || client == nil {
		t.Fatalf("explicitly enabled New = %v, %v", client, err)
	}
	if options.Config.Address != server.URL || options.Config.Scheme != "" ||
		options.Config.Transport != nil || options.Config.HttpClient != nil {
		t.Fatalf("New changed caller config: %#v", options.Config)
	}
}

func TestNewRejectsMissingConfigWhenEnabled(t *testing.T) {
	client, cleanup, err := New(testLogger(t), Options{})
	if err == nil || client != nil {
		t.Fatalf("New without config = %v, %v, want an error", client, err)
	}
	cleanup()
}

func TestNewRejectsEmptyAddressBeforeProbe(t *testing.T) {
	for _, address := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("address=%q", address), func(t *testing.T) {
			client, cleanup, err := New(testLogger(t), Options{Config: &api.Config{
				Address:    address,
				HttpClient: &http.Client{Transport: rejectProbeTransport{t: t}},
			}})
			defer cleanup()
			if client != nil || err == nil || !strings.Contains(err.Error(), "consul address is empty") {
				t.Fatalf("New with empty address = %v, %v, want consul address is empty", client, err)
			}
		})
	}
}

type rejectProbeTransport struct {
	t *testing.T
}

func (r rejectProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Error("empty address triggered a network probe")
	return nil, errors.New("unexpected probe")
}

func TestNewSkipsWhenLocalAddressIsEmpty(t *testing.T) {
	t.Setenv(DisableConsul, "false")
	t.Setenv("APP_ENV", "local")
	t.Setenv(api.HTTPAddrEnvName, "")
	client, cleanup, err := New(testLogger(t), NewOptions())
	if err != nil || client != nil {
		t.Fatalf("local no-address New = %v, %v", client, err)
	}
	cleanup()
}

func TestNewSkipsWhenLocalAddressIsUnset(t *testing.T) {
	t.Setenv(DisableConsul, "false")
	t.Setenv("APP_ENV", "local")
	previous, wasSet := os.LookupEnv(api.HTTPAddrEnvName)
	if err := os.Unsetenv(api.HTTPAddrEnvName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(api.HTTPAddrEnvName, previous)
			return
		}
		_ = os.Unsetenv(api.HTTPAddrEnvName)
	})

	client, cleanup, err := New(testLogger(t), NewOptions())
	if err != nil || client != nil {
		t.Fatalf("local unset-address New = %v, %v", client, err)
	}
	cleanup()
}

func TestNewProbesLocalLeaderAndRejectsEmptyOrError(t *testing.T) {
	for _, result := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{name: "leader", body: `"127.0.0.1:8300"`},
		{name: "empty", body: `""`, wantErr: true},
		{name: "error", body: "failure", status: http.StatusServiceUnavailable, wantErr: true},
	} {
		t.Run(result.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/status/leader" {
					t.Errorf("path = %q", r.URL.Path)
				}
				if result.status != 0 {
					w.WriteHeader(result.status)
				}
				_, _ = w.Write([]byte(result.body))
			}))
			defer server.Close()
			t.Setenv(DisableConsul, "false")
			t.Setenv("APP_ENV", "dev")
			t.Setenv(api.HTTPAddrEnvName, server.Listener.Addr().String())
			client, cleanup, err := New(testLogger(t), NewOptions())
			defer cleanup()
			if result.wantErr && err == nil {
				t.Fatal("New() succeeded")
			}
			if !result.wantErr && (err != nil || client == nil) {
				t.Fatalf("New() = %v, %v", client, err)
			}
		})
	}
}

func TestNewReturnsDisabledClientWithoutNetworkProbe(t *testing.T) {
	t.Setenv(DisableConsul, "true")
	shared, release, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	client, cleanup, err := New(shared, NewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if client != nil {
		t.Fatalf("New disabled Consul client = %v, want nil", client)
	}
	if cleanup == nil {
		t.Fatal("New disabled Consul returned nil cleanup")
	}
	cleanup()
	cleanup()
}

func TestNewKeepsHostnameForEveryDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`"leader:8300"`)) }))
	defer server.Close()
	var addresses []string
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	client, cleanup, err := New(testLogger(t), Options{Config: &api.Config{Address: "http://consul.invalid:8500", Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := client.Status().Leader(); err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 {
		t.Fatalf("dial count=%d", len(addresses))
	}
	for _, address := range addresses {
		if address != "consul.invalid:8500" {
			t.Fatalf("hostname was pinned to %q", address)
		}
	}
}

type addressProbeTransport func(*http.Request) (*http.Response, error)

func (f addressProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewPreservesHostnameAndSDKAddressOptions(t *testing.T) {
	for _, test := range []struct{ name, address, scheme, wantScheme, wantHost, wantPath string }{
		{"default scheme and port", "consul.internal:8500", "", "http", "consul.internal:8500", "/v1/status/leader"},
		{"configured scheme", "consul.internal", "https", "https", "consul.internal", "/v1/status/leader"},
		{"address scheme and prefix", "https://consul.internal:8501/prefix", "", "https", "consul.internal:8501", "/prefix/v1/status/leader"},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := addressProbeTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Scheme != test.wantScheme || request.URL.Host != test.wantHost || request.URL.Path != test.wantPath {
					t.Fatalf("probe URL=%s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`"leader:8300"`))}, nil
			})
			_, cleanup, err := New(testLogger(t), Options{Config: &api.Config{Address: test.address, Scheme: test.scheme, HttpClient: &http.Client{Transport: transport}}})
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
		})
	}
}
