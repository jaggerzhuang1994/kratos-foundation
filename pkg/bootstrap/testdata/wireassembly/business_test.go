package wireassembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
)

func businessSources(t *testing.T) config.Sources {
	t.Helper()
	connections := map[string]any{}
	for _, name := range []string{"default", "audit"} {
		connections[name] = map[string]any{"driver": "sqlite3", "dsn": filepath.Join(t.TempDir(), name+".db")}
	}
	content, err := json.Marshal(map[string]any{
		"database": map[string]any{"default": "default", "connections": connections,
			"metrics": map[string]any{"disable": true}, "tracing": map[string]any{"disable": true}},
		"tracing": map[string]any{"disable": true},
		"server":  map[string]any{"http": map[string]any{"addr": "127.0.0.1:0"}, "stop_delay": "0s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := textconfig.NewSource("business.json", config.JSONFormat, string(content))
	if err != nil {
		t.Fatal(err)
	}
	return config.NewSources(source)
}

func TestBusinessHTTPTransactionsAndCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	built, cleanup, err := initialize(ctx, businessSources(t), "business-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	for _, name := range []string{"default", "audit"} {
		if err := built.Database.Connection(database.UseConnection(ctx, name)).AutoMigrate(&order{}); err != nil {
			t.Fatal(err)
		}
	}
	httpRuntime, _ := built.Server.Servers()
	endpoint, err := httpRuntime.(transport.Endpointer).Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	// App.Run 拥有运行协程；取消父 Context 后等待它退出，再允许 Wire cleanup 释放数据库。
	done := make(chan error, 1)
	go func() { done <- built.App.Run() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			stopped = true
			if err != nil {
				t.Errorf("App.Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("application did not stop after parent cancellation")
		}
	}
	t.Cleanup(stop)
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	deadline := time.Now().Add(5 * time.Second)
	for !built.Spec.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("application never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		response, err := client.Get(endpoint.String() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != 200 {
			t.Fatalf("probe %s: status=%d read=%v close=%v", path, response.StatusCode, readErr, closeErr)
		}
		if path == "/metrics" && !strings.Contains(string(body), "foundation_config_watcher_up 1") {
			t.Fatal("configuration metrics absent from HTTP endpoint")
		}
	}
	for _, tt := range []struct {
		name   string
		body   string
		status int
	}{
		{"commit", `{"id":1,"amount":10}`, http.StatusCreated},
		{"rollback", `{"id":2,"amount":-1}`, http.StatusUnprocessableEntity},
		{"named connection", `{"id":3,"amount":20,"connection":"audit"}`, http.StatusCreated},
		{"unknown connection", `{"id":4,"amount":20,"connection":"missing"}`, http.StatusInternalServerError},
		{"invalid input", `{"id":`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, err := client.Post(endpoint.String()+"/orders", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read response: %v; close: %v", readErr, closeErr)
			}
			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, tt.status, body)
			}
			if tt.status == http.StatusInternalServerError && string(body) != "order failed\n" {
				t.Fatalf("unexpected public error response: %q", body)
			}
		})
	}
	for name, wantID := range map[string]int64{"default": 1, "audit": 3} {
		var orders []order
		if err := built.Database.Connection(database.UseConnection(context.Background(), name)).Find(&orders).Error; err != nil {
			t.Fatal(err)
		}
		if len(orders) != 1 || orders[0].ID != wantID {
			t.Fatalf("%s persisted orders = %+v, want only ID %d", name, orders, wantID)
		}
	}
	stop()
	if built.Spec.Ready() {
		t.Fatal("stopped application still ready")
	}
	// 停止运行时不应提前关闭数据库；Wire cleanup 才是连接池所有者。
	if err := built.Database.Connection(context.Background()).Exec("SELECT 1").Error; err != nil {
		t.Fatalf("database closed before Wire cleanup: %v", err)
	}
	cleanup()
	cleanup()
	if err := built.Database.Connection(context.Background()).Error; !errors.Is(err, database.ErrManagerClosed) {
		t.Fatalf("database after cleanup: %v", err)
	}
}

func TestBusinessRejectsRemovedConfig(t *testing.T) {
	for _, legacy := range []string{
		`{"log":{}}`, `{"metrics":{}}`, `{"job":{}}`,
		`{"server":{"middleware":{"timeout":{}}}}`,
		`{"database":{"connections":{"default":{"replicas":[]}}}}`,
	} {
		t.Run(legacy, func(t *testing.T) {
			source, err := textconfig.NewSource("legacy.json", config.JSONFormat, legacy)
			if err != nil {
				t.Fatal(err)
			}
			_, cleanup, err := initialize(context.Background(), config.NewSources(source), "legacy-test", 0)
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			if !errors.Is(err, config.ErrRemovedField) {
				t.Fatalf("legacy config should fail explicitly: %v", err)
			}
		})
	}
}

func TestMonitoringBindFailureStopsApplication(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Error(err)
		}
	}()
	source, err := textconfig.NewSource("monitoring.json", config.JSONFormat, fmt.Sprintf(`{"server":{"http":{"metrics":{"addr":%q}}}}`, occupied.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	sources := append(businessSources(t), source)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	built, cleanup, err := initialize(ctx, sources, "monitoring-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	done := make(chan error, 1)
	go func() { done <- built.App.Run() }()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("monitoring bind failure not propagated: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("application did not stop after monitoring failure")
	}
	if built.Spec.Ready() {
		t.Fatal("failed application still ready")
	}
}
