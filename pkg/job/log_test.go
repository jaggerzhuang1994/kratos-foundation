package job

import (
	"context"
	"errors"
	"fmt"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCronLoggerTranslatesFieldsAndSuppressesDuplicateEvents(t *testing.T) {
	logger, logPath := testFileFoundationLogger(t)
	cronLog := newCronLog(logger, managerOptions{LoggingEnabled: true})
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

// TestDisabledJobLoggerKeepsDerivedLogsSilent 防止派生操作绕过任务显式关闭的日志开关。
func TestDisabledJobLoggerKeepsDerivedLogsSilent(t *testing.T) {
	logger, path := testFileFoundationLogger(t)
	disabled := newJobLog(logger, managerOptions{})
	derived := disabled.With("key", "value").
		WithModule("job/custom").
		WithContext(context.Background()).
		WithLevel(kratoslog.LevelDebug).
		WithCallerDepth(2).
		WithFilterKeys("secret")
	if err := derived.Log(kratoslog.LevelInfo, "msg", "disabled"); err != nil {
		t.Fatal(err)
	}
	value := disabledLogStringer{t: t}
	derived.Debug(value)
	derived.Debugf("%s", value)
	derived.Debugw("value", value)
	derived.Info(value)
	derived.Infof("%s", value)
	derived.Infow("value", value)
	derived.Warn(value)
	derived.Warnf("%s", value)
	derived.Warnw("value", value)
	derived.Error(value)
	derived.Errorf("%s", value)
	derived.Errorw("value", value)
	if written, err := os.ReadFile(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	} else if len(written) != 0 {
		t.Fatalf("disabled logger wrote %q", written)
	}
}

type disabledLogStringer struct{ t *testing.T }

func (v disabledLogStringer) String() string {
	v.t.Error("disabled logger formatted a value")
	return "unexpected"
}

func TestDisabledJobLoggerFatalExits(t *testing.T) {
	const helperEnv = "KRATOS_FOUNDATION_DISABLED_JOB_FATAL"
	if mode := os.Getenv(helperEnv); mode != "" {
		logger := &disabledLogger{}
		value := disabledLogStringer{t: t}
		switch mode {
		case "fatal":
			logger.Fatal(value)
		case "fatalf":
			logger.Fatalf("%s", value)
		case "fatalw":
			logger.Fatalw("value", value)
		}
		t.Fatal("fatal returned without exiting")
	}
	for _, mode := range []string{"fatal", "fatalf", "fatalw"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestDisabledJobLoggerFatalExits$")
			command.Env = append(os.Environ(), helperEnv+"="+mode)
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("fatal error=%v output=%q, want exit 1", err, output)
			}
			if len(output) != 0 {
				t.Fatalf("disabled fatal produced output: %q", output)
			}
		})
	}
}
