package job

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCronLoggerTranslatesFieldsAndSuppressesDuplicateEvents(t *testing.T) {
	logger, logPath := testFileFoundationLogger(t)
	cronLog, err := newCronLog(logger, managerOptions{LoggingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	loggerAdapter := newCronLogger(cronLog)
	now := time.Date(2026, 8, 31, 11, 12, 13, 0, time.UTC)
	values := []any{"now", now, "next", now.Add(time.Hour)}
	loggerAdapter.Info("wake", values...)
	_, _, line, _ := runtime.Caller(0)
	loggerAdapter.Info("retire", "next", now.Add(2*time.Hour))
	loggerAdapter.Info("schedule", "marker", "must-not-be-written")
	loggerAdapter.Error(errors.New("scheduler failed"), "cron error", "next", now.Add(3*time.Hour))

	if _, ok := values[1].(time.Time); !ok {
		t.Fatalf("cron logger modified caller values: %#v", values)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, fragment := range []string{
		fmt.Sprintf("caller=job/log_test.go:%d", line+1),
		"DEBUG ",
		"msg=wake",
		"msg=retire",
		"msg=cron error",
		"error=scheduler failed",
		"next=2026-08-31T12:12:13Z",
	} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("cron adapter log lacks %q: %s", fragment, logs)
		}
	}
	if strings.Contains(logs, "must-not-be-written") {
		t.Fatalf("cron adapter emitted a suppressed event: %s", logs)
	}
}
