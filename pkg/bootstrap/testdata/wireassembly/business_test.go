package wireassembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
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
	built, cleanup, err := initialize(businessSources(t), "business-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	for _, name := range []string{"default", "audit"} {
		if err := built.Database.Connection(database.UseConnection(ctx, name)).AutoMigrate(&order{}); err != nil {
			t.Fatal(err)
		}
	}
	// App.Run 拥有运行协程；显式停止后等待退出，再允许 Wire cleanup 释放数据库。
	done := make(chan error, 1)
	go func() { done <- built.App.Run() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		if err := built.App.Stop(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			stopped = true
			if err != nil {
				t.Errorf("App.Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("application did not stop after Stop")
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
	// 监听由 Foundation Runtime 准备，业务从 App 已解析的发现地址读取端点。
	endpoints := built.App.Endpoint()
	if len(endpoints) != 1 {
		t.Fatalf("application endpoints = %v", endpoints)
	}
	endpoint := endpoints[0]
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		response, err := client.Get(endpoint + path)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != 200 {
			t.Fatalf("probe %s: status=%d read=%v close=%v", path, response.StatusCode, readErr, closeErr)
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
			response, err := client.Post(endpoint+"/orders", "application/json", strings.NewReader(tt.body))
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
	built, cleanup, err := initialize(sources, "monitoring-test", nil)
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

// composeConfig 生成组合场景的 YAML 配置；stopTimeout 与 loggingDisable 用于驱动热更新。
func composeConfig(dir, stopTimeout string, loggingDisable bool) string {
	return fmt.Sprintf(`app:
  stop_timeout: %s
database:
  default: default
  connections:
    default:
      driver: sqlite3
      dsn: %q
  metrics:
    disable: true
  tracing:
    disable: true
tracing:
  disable: true
server:
  stop_delay: 0s
  logging:
    disable: %t
  http:
    addr: 127.0.0.1:0
`, stopTimeout, filepath.Join(dir, "compose.db"), loggingDisable)
}

// waitForLog 轮询日志文件直到 match 成立；配置热更新经文件监听与 20ms 轮询异步生效。
func waitForLog(t *testing.T, path string, match func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if match(string(data)) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("log condition not met:\n%s", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func logLines(content, event string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "event="+event+" ") || strings.HasSuffix(line, "event="+event) {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestComposedLifecycleHotReloadAndDiagnostics 组合验证：真实文件配置源热更新、HTTP 与数据库事务、
// 常驻任务的 Ready 屏障、业务日志 caller、错误诊断出口的环境约束，以及热更新预算在停机时生效。
func TestComposedLifecycleHotReloadAndDiagnostics(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	t.Setenv("CONFIG_POLL_INTERVAL", "20ms")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FILE_ENABLE", "true")
	t.Setenv("LOG_FILE_PATH", logPath)
	t.Setenv("LOG_FILE_ROTATING_DISABLE", "true")
	t.Setenv("APP_ENV", "local")
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(composeConfig(dir, "5s", false)), 0o600); err != nil {
		t.Fatal(err)
	}
	fileSources, err := file.NewSources(file.PathList{configPath})
	if err != nil {
		t.Fatal(err)
	}
	built, cleanup, err := initialize(config.NewSources(fileSources...), "compose-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			cleanup()
		}
	})
	if err := built.Database.Connection(context.Background()).AutoMigrate(&order{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- built.App.Run() }()
	returned := false
	t.Cleanup(func() {
		// 断言中途失败时仍停止应用；正常路径已在测试体内等待 Run 返回。
		if !returned {
			_ = built.App.Stop()
			<-done
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !built.Spec.Ready() || !built.Server.daemonStarted.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("ready=%v daemon=%v", built.Spec.Ready(), built.Server.daemonStarted.Load())
		}
		time.Sleep(time.Millisecond)
	}
	// Job Runtime 等待 Ready 信号，任务启动时应用必须已经就绪。
	if !built.Server.daemonStartedReady.Load() {
		t.Fatal("daemon job started before application ready")
	}
	endpoint := built.App.Endpoint()[0]
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	get := func(path string) (int, []byte) {
		t.Helper()
		response, err := client.Get(endpoint + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	if status, _ := get("/readyz"); status != http.StatusOK {
		t.Fatalf("readyz = %d", status)
	}

	// 业务事务与业务日志：caller 必须指向业务源码中的记录行。
	response, err := client.Post(endpoint+"/orders", "application/json", strings.NewReader(`{"id":7,"amount":10}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("order status = %d", response.StatusCode)
	}
	source, err := os.ReadFile("business.go")
	if err != nil {
		t.Fatal(err)
	}
	markerLine := 0
	for index, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, "// compose:caller") {
			markerLine = index + 1
		}
	}
	if markerLine == 0 {
		t.Fatal("caller marker not found")
	}
	// wire 测试把源文件复制到临时模块根目录，caller 的目录段随之变化，只校验文件与行号。
	wantCaller := fmt.Sprintf("business.go:%d", markerLine)
	content := waitForLog(t, logPath, func(content string) bool { return len(logLines(content, "demo.order.created")) == 1 })
	orderLine := logLines(content, "demo.order.created")[0]
	if !strings.Contains(orderLine, wantCaller+" ") || !strings.Contains(orderLine, "order.id=7") {
		t.Fatalf("business log = %q, want %s", orderLine, wantCaller)
	}

	// 诊断出口：local 返回 err_stack，prod 剥离；任何环境都不暴露 cause 原文。
	for _, environment := range []string{"local", "prod"} {
		t.Setenv("APP_ENV", environment)
		status, body := get("/fail")
		var payload struct {
			Message  string            `json:"message"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("%s: %v %s", environment, err, body)
		}
		_, hasStack := payload.Metadata["err_stack"]
		if status != http.StatusInternalServerError || hasStack != (environment == "local") || strings.Contains(string(body), "private-cause") {
			t.Fatalf("%s: status=%d stack=%v body=%s", environment, status, hasStack, body)
		}
	}
	t.Setenv("APP_ENV", "local")

	// 热更新：同时改动停机预算与 server logging，整轮只输出一条 INFO 变化事件。
	if err := os.WriteFile(configPath, []byte(composeConfig(dir, "7s", true)), 0o600); err != nil {
		t.Fatal(err)
	}
	content = waitForLog(t, logPath, func(content string) bool {
		return len(logLines(content, "app.stop_policy.updated")) > 0 && len(logLines(content, "server.middleware.updated")) > 0
	})
	changed := logLines(content, "config.snapshot.changed")
	if len(changed) != 1 || !strings.Contains(changed[0], "app.stop_timeout") || !strings.Contains(changed[0], "server.logging.disable") || !strings.HasPrefix(changed[0], "INFO") {
		t.Fatalf("snapshot change events = %q", changed)
	}
	if updated := logLines(content, "server.middleware.updated"); len(updated) != 1 || !strings.Contains(updated[0], "changed=[logging]") {
		t.Fatalf("middleware update events = %q", updated)
	}
	if updated := logLines(content, "app.stop_policy.updated"); len(updated) != 1 || !strings.Contains(updated[0], "stop_timeout=7s") || !strings.Contains(updated[0], "previous=5s") {
		t.Fatalf("stop policy events = %q", updated)
	}
	for _, legacy := range []string{"config.change", "config.watch"} {
		if lines := logLines(content, legacy); len(lines) != 0 {
			t.Fatalf("legacy event %s still logged: %q", legacy, lines)
		}
	}
	for _, secret := range []string{"private-cause", "password="} {
		if strings.Contains(content, secret) {
			t.Fatalf("log leaked %q", secret)
		}
	}

	// 停机：Ready 立即撤销，任务收到取消，冻结的是热更新后的预算，运行结果为成功。
	if err := built.App.Stop(); err != nil {
		t.Fatal(err)
	}
	if built.Spec.Ready() {
		t.Fatal("application still ready after Stop")
	}
	select {
	case err := <-done:
		returned = true
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("application did not stop")
	}
	if !built.Server.daemonStopped.Load() {
		t.Fatal("daemon job did not observe cancellation")
	}
	cleanup()
	cleaned = true
	content = waitForLog(t, logPath, func(content string) bool { return len(logLines(content, "app.stopped")) == 1 })
	if stopping := logLines(content, "app.stopping"); len(stopping) != 1 || !strings.Contains(stopping[0], "stop_timeout=7s") {
		t.Fatalf("stopping events = %q", stopping)
	}
	if stopped := logLines(content, "app.stopped"); !strings.Contains(stopped[0], "result=success") {
		t.Fatalf("stopped event = %q", stopped)
	}
	for _, event := range []string{"app.assembled", "app.ready", "app.stopping", "app.stopped"} {
		if lines := logLines(content, event); len(lines) != 1 {
			t.Fatalf("%s logged %d times", event, len(lines))
		}
	}
}
