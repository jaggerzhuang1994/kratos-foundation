package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// panic 日志不受级别限制：根级别与输出端级别都是 FATAL、event 被 filter_keys 过滤时，
// 普通 ERROR 不输出，事件名表示 panic 的 ERROR 仍然输出；低于 ERROR 的同名事件不放行。
func TestPanicEventsBypassLevelThresholds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panic.log")
	for key, value := range map[string]string{
		EnvLevel: "fatal", EnvStdDisable: "true", EnvFilterKeys: "event",
		EnvFileEnable: "true", EnvFileLevel: "fatal", EnvFilePath: path, EnvFileRotatingDisable: "true",
	} {
		t.Setenv(key, value)
	}
	logger, cleanup, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	logger.With("event", "server.request.failed").Error("ordinary failure")
	logger.Errorw("event", "server.request.failed", "msg", "ordinary keyvals failure")
	logger.With("event", "server.request.panic.recovered").Warn("panic event below error")
	logger.With("event", "server.request.panic.recovered").Error("bound panic event")
	logger.With("event", "queue.task.panic.recovered").Errorf("formatted %s", "panic event")
	logger.Errorw("event", "app.runtime.panicked", "msg", "keyvals panic event")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(data)
	for _, want := range []string{"bound panic event", "formatted panic event", "keyvals panic event"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("panic log %q missing at FATAL level: %s", want, logs)
		}
	}
	for _, unwanted := range []string{"ordinary failure", "ordinary keyvals failure", "panic event below error"} {
		if strings.Contains(logs, unwanted) {
			t.Fatalf("log %q must be filtered at FATAL level: %s", unwanted, logs)
		}
	}
	if strings.Count(logs, "\n") != 3 {
		t.Fatalf("want exactly 3 lines: %s", logs)
	}
}
