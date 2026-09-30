package consul

import (
	"fmt"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewClientDisabled(t *testing.T) {
	for _, test := range []struct{ name, disabled, address string }{
		{"explicit", "true", ":invalid:"},
		{"local without address", "false", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", "local")
			t.Setenv("DISABLE_CONSUL", test.disabled)
			t.Setenv("CONSUL_HTTP_ADDR", test.address)
			client, disabled, err := newClient()
			if client != nil || !disabled || err != nil {
				t.Fatalf("disabled initialization: %v %v %v", client, disabled, err)
			}
		})
	}
}

func TestNewClientInitialProbe(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"leader", `"leader:8300"`, http.StatusOK, false},
		{"no elected leader", `""`, http.StatusOK, true},
		{"unavailable", "failed", http.StatusServiceUnavailable, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/status/leader" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(test.status)
				if _, err := w.Write([]byte(test.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("DISABLE_CONSUL", "false")
			t.Setenv("APP_ENV", "dev")
			t.Setenv("CONSUL_HTTP_ADDR", server.URL)
			client, disabled, err := newClient()
			if disabled || (err != nil) != test.wantErr || (client == nil) != test.wantErr {
				t.Fatalf("probe result: %v %v %v", client, disabled, err)
			}
		})
	}
}

func TestNewClientRejectsInvalidEnvironment(t *testing.T) {
	for _, address := range []string{" ", "ftp://consul.invalid:8500"} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("APP_ENV", "dev")
			t.Setenv("DISABLE_CONSUL", "false")
			t.Setenv("CONSUL_HTTP_ADDR", address)
			client, disabled, err := newClient()
			if client != nil || disabled || err == nil {
				t.Fatalf("invalid address accepted: %v %v %v", client, disabled, err)
			}
		})
	}
}

func TestNewClientPreservesHostnameAndToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "localhost:") {
			t.Errorf("hostname changed: %s", r.Host)
		}
		if r.Header.Get("X-Consul-Token") != "test-token" {
			t.Error("env token missing")
		}
		calls.Add(1)
		if _, err := w.Write([]byte(`"leader:8300"`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("DISABLE_CONSUL", "false")
	t.Setenv("CONSUL_HTTP_ADDR", strings.Replace(server.URL, "127.0.0.1", "localhost", 1))
	t.Setenv("CONSUL_HTTP_TOKEN", "test-token")
	client, disabled, err := newClient()
	if err != nil || disabled {
		t.Fatalf("initialization: %v", err)
	}
	if _, err := client.Status().Leader(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("requests %d", calls.Load())
	}
}

func TestSingletonConcurrentInitializationAndEnvFreeze(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		_, _ = w.Write([]byte(`"leader:8300"`))
	}))
	defer server.Close()
	t.Setenv("DISABLE_CONSUL", "false")
	t.Setenv("CONSUL_HTTP_ADDR", server.URL)
	const count = 32
	clients := make(chan Client, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			client, disabled, err := Get()
			if err != nil || disabled {
				t.Error(err)
			}
			clients <- client
		})
	}
	group.Wait()
	close(clients)
	var first Client
	for client := range clients {
		if first == nil {
			first = client
		}
		if client != first {
			t.Fatal("multiple singleton instances")
		}
	}
	if first == nil || probes.Load() != 1 {
		t.Fatalf("client %v, probes %d", first, probes.Load())
	}
	t.Setenv("DISABLE_CONSUL", "true")
	t.Setenv("CONSUL_HTTP_ADDR", ":invalid:")
	again, disabled, err := Get()
	if err != nil || disabled || again != first || probes.Load() != 1 {
		t.Fatalf("singleton reinitialized: %v", err)
	}
}

func TestSingletonCachesFailureAndDisabledState(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			var probes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probes.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			t.Setenv("DISABLE_CONSUL", fmt.Sprint(disabled))
			t.Setenv("CONSUL_HTTP_ADDR", server.URL)
			var shared singleton
			client, firstDisabled, first := shared.get()
			// 修改 env 不能改变已经缓存的禁用/失败结果。
			t.Setenv("DISABLE_CONSUL", fmt.Sprint(!disabled))
			_, secondDisabled, second := shared.get()
			if client != nil || firstDisabled != disabled || secondDisabled != disabled || first != second || (first == nil) != disabled {
				t.Fatalf("unexpected cached result: client=%v disabled=%v/%v err=%v/%v", client, firstDisabled, secondDisabled, first, second)
			}
			want := int32(1)
			if disabled {
				want = 0
			}
			if probes.Load() != want {
				t.Fatalf("probes %d want %d", probes.Load(), want)
			}
		})
	}
}

// 初始化失败由调用边界处理；这里验证安全字段及预期禁用的级别，不记录 env 原地址。
func TestNewClientLogsSafeLifecycleEvents(t *testing.T) {
	var records []consulCapturedLog
	restore := foundationlog.SetLogger(consulCaptureLogger{records: &records})
	t.Cleanup(restore)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DISABLE_CONSUL", "true")
	t.Setenv("CONSUL_HTTP_ADDR", "http://test-user:test-secret@127.0.0.1:1")
	if _, disabled, err := newClient(); err != nil || !disabled {
		t.Fatalf("disabled=%v err=%v", disabled, err)
	}
	if len(records) != 1 || records[0].level != kratoslog.LevelInfo || records[0].fields["event"] != "consul.client.disabled" {
		t.Fatalf("disabled logs=%+v", records)
	}
	t.Setenv("DISABLE_CONSUL", "false")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`"leader:8300"`)) }))
	defer server.Close()
	t.Setenv("CONSUL_HTTP_ADDR", server.URL)
	if _, disabled, err := newClient(); err != nil || disabled {
		t.Fatalf("ready=%v err=%v", disabled, err)
	}
	if len(records) != 3 || records[1].fields["event"] != "consul.client.probing" || records[1].fields["timeout"] != probeTimeout || records[2].fields["event"] != "consul.client.ready" {
		t.Fatalf("ready logs=%+v", records)
	}
	// 不支持的 scheme 在 SDK 构造期拒绝，不会访问带用户信息的远端地址。
	t.Setenv("CONSUL_HTTP_ADDR", "ftp://test-user:test-secret@consul.invalid:8500")
	if _, _, err := newClient(); err == nil {
		t.Fatal("unsupported address scheme accepted")
	}
	for _, record := range records {
		if record.level != kratoslog.LevelInfo || record.fields["module"] != "consul" || record.fields["address"] != nil {
			t.Fatalf("unsafe lifecycle fields=%+v", record)
		}
		text := fmt.Sprint(record.fields)
		if strings.Contains(text, "test-user") || strings.Contains(text, "test-secret") {
			t.Fatalf("credentials in log: %s", text)
		}
	}
}

type consulCapturedLog struct {
	level  kratoslog.Level
	fields map[string]any
}
type consulCaptureLogger struct{ records *[]consulCapturedLog }

func (l consulCaptureLogger) Log(level kratoslog.Level, keyvals ...any) error {
	fields := make(map[string]any, len(keyvals)/2)
	for i := 0; i+1 < len(keyvals); i += 2 {
		fields[keyvals[i].(string)] = keyvals[i+1]
	}
	*l.records = append(*l.records, consulCapturedLog{level: level, fields: fields})
	return nil
}
