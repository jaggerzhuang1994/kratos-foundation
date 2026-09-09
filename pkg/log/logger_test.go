package log

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

type capturedLogRecord struct {
	level   kratoslog.Level
	keyvals []any
}

func TestLoggerHelperMethodsEmitExpectedLevelsAndPayloads(t *testing.T) {
	var records []capturedLogRecord
	shared := &SharedState{}
	shared.config.Store(&configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			records = append(records, capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)})
			return nil
		})},
	})
	shared.custom.Store(&customState{})
	logger := &logger{shared: shared}

	logger.Debug("debug")
	logger.Debugf("debug-%d", 2)
	logger.Debugw("event", "debugw")
	logger.Info("info")
	logger.Infof("info-%d", 2)
	logger.Infow("event", "infow")
	logger.Warn("warn")
	logger.Warnf("warn-%d", 2)
	logger.Warnw("event", "warnw")
	logger.Error("error")
	logger.Errorf("error-%d", 2)
	logger.Errorw("event", "errorw")

	wantLevels := []kratoslog.Level{
		kratoslog.LevelDebug, kratoslog.LevelDebug, kratoslog.LevelDebug,
		kratoslog.LevelInfo, kratoslog.LevelInfo, kratoslog.LevelInfo,
		kratoslog.LevelWarn, kratoslog.LevelWarn, kratoslog.LevelWarn,
		kratoslog.LevelError, kratoslog.LevelError, kratoslog.LevelError,
	}
	if len(records) != len(wantLevels) {
		t.Fatalf("records = %d, want %d", len(records), len(wantLevels))
	}
	for index, wantLevel := range wantLevels {
		if records[index].level != wantLevel {
			t.Errorf("record %d level = %v, want %v", index, records[index].level, wantLevel)
		}
	}
	for index, want := range []string{"debug", "debug-2", "debugw", "info", "info-2", "infow", "warn", "warn-2", "warnw", "error", "error-2", "errorw"} {
		if !containsLogValue(records[index].keyvals, want) {
			t.Errorf("record %d lacks %q: %#v", index, want, records[index].keyvals)
		}
	}
}

func TestDerivedLoggerAppliesContextModuleOverrideAndFilters(t *testing.T) {
	var mu sync.Mutex
	var written []any
	shared := &SharedState{}
	shared.config.Store(&configState{
		level:       kratoslog.LevelInfo,
		filterEmpty: false,
		msgKey:      defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, keyvals ...any) error {
			mu.Lock()
			defer mu.Unlock()
			written = append([]any(nil), keyvals...)
			return nil
		})},
	})
	shared.custom.Store(&customState{})
	if err := shared.WithFilterEmpty(true); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterKeys("global.secret"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV("global", "value"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithCallerDepth(1); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithTimeFormat("2006"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithMsgKey("message"); err != nil {
		t.Fatal(err)
	}
	ctx := WithKv(context.Background(), "request.id", "r1")
	contextCopy := KvFromCtx(ctx)
	contextCopy[1] = "mutated"
	if KvFromCtx(ctx)[1] != "r1" {
		t.Fatal("KvFromCtx returned aliased state")
	}
	derived, err := NewLogger(shared).WithModuleConfig("orders", testModuleConfig{
		level:      "debug",
		filterKeys: []string{"module.secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	derived = derived.
		WithContext(ctx).
		WithCallerDepth(2).
		AddCallerDepth(1).
		WithFilterKeys("local.secret").
		With("fixed", "yes", "empty", "")
	derived.Infow(
		"event", "created",
		"global.secret", "hidden",
		"module.secret", "hidden",
		"local.secret", "hidden",
	)

	mu.Lock()
	got := append([]any(nil), written...)
	mu.Unlock()
	for _, want := range []any{"orders", "r1", "value", "yes", "created"} {
		if !containsLogValue(got, want) {
			t.Errorf("derived log lacks %#v: %#v", want, got)
		}
	}
	for _, hidden := range []any{"hidden", "empty"} {
		if containsLogValue(got, hidden) {
			t.Errorf("derived log retained filtered value %#v: %#v", hidden, got)
		}
	}
}

func containsLogValue(keyvals []any, want any) bool {
	for _, value := range keyvals {
		if reflect.DeepEqual(value, want) || fmt.Sprint(value) == fmt.Sprint(want) {
			return true
		}
	}
	return false
}

type testModuleConfig struct {
	disable    bool
	level      string
	filterKeys []string
}

func (c testModuleConfig) GetDisable() bool { return c.disable }

func (c testModuleConfig) GetLevel() string { return c.level }

func (c testModuleConfig) GetFilterKeys() []string { return c.filterKeys }

func TestWithModuleConfigRejectsInvalidInput(t *testing.T) {
	l := &logger{}
	tests := []struct {
		name   string
		module string
		config ModuleConfig
	}{
		{name: "empty module", module: ""},
		{name: "module surrounding whitespace", module: " orders "},
		{name: "invalid level", module: "orders", config: testModuleConfig{level: "verbose"}},
		{name: "empty filter key", module: "orders", config: testModuleConfig{filterKeys: []string{""}}},
		{name: "filter key surrounding whitespace", module: "orders", config: testModuleConfig{filterKeys: []string{" token"}}},
		{name: "duplicate filter key", module: "orders", config: testModuleConfig{filterKeys: []string{"token", "token"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := l.WithModuleConfig(test.module, test.config); err == nil {
				t.Fatal("WithModuleConfig() error = nil, want validation error")
			}
		})
	}
}

func TestWithModulePanicsForInvalidConstant(t *testing.T) {
	for _, module := range []string{"", " orders "} {
		t.Run(module, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("WithModule() did not panic")
				}
			}()
			(&logger{}).WithModule(module)
		})
	}
}

func TestPublicLoggerConstructsLogger(t *testing.T) {
	config := Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: FileConfig{
			OutputConfig: OutputConfig{
				Disable: true,
				Level:   kratoslog.LevelInfo,
			},
		},
	}
	shared, cleanup, err := NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if logger := NewLogger(shared); logger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

func TestPublicLoggerAppliesContextOverridesAndRuntimeUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundation.log")
	config := Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: FileConfig{
			OutputConfig: OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     RotatingConfig{Disable: true},
		},
	}
	shared, cleanup, err := NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	ctx := WithKv(context.Background(), "request.id", "r1")
	ctx = WithKv(ctx, "tenant", "acme")
	contextFields := KvFromCtx(ctx)
	contextFields[1] = "mutated"
	if got := KvFromCtx(ctx); len(got) != 4 || got[1] != "r1" || got[3] != "acme" {
		t.Fatalf("context fields were not appended and copied: %#v", got)
	}
	if err := shared.WithLevel(kratoslog.LevelDebug); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterEmpty(true); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterKeys("secret"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV("global", "value"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithCallerDepth(1); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithTimeFormat("2006-01-02"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithMsgKey("message"); err != nil {
		t.Fatal(err)
	}
	logger := NewLogger(shared).
		WithModule("orders").
		WithContext(ctx).
		With("secret", "private", "empty", "")
	logger.Info("created")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(written)
	for _, fragment := range []string{"module=orders", "request.id=r1", "tenant=acme", "global=value", "message=created"} {
		if !strings.Contains(line, fragment) {
			t.Fatalf("log line lacks %q: %s", fragment, line)
		}
	}
	if strings.Contains(line, "private") || strings.Contains(line, "empty=") {
		t.Fatalf("log filters were not applied: %s", line)
	}

	if err := shared.WithLevel(kratoslog.LevelError); err != nil {
		t.Fatal(err)
	}
	config.Level = kratoslog.LevelError
	if err := NewUpdateLogger(shared).Update(config); err != nil {
		t.Fatal(err)
	}
	before := len(written)
	logger.Info("filtered")
	written, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != before {
		t.Fatalf("runtime level update did not filter info log: %s", written[before:])
	}
}

const fatalHelperEnvironment = "KRATOS_FOUNDATION_FATAL_HELPER"

func TestLoggerFatalMethodsLogAndExit(t *testing.T) {
	tests := map[string]string{
		"fatal":  "fatal-value",
		"fatalf": "fatalf-42",
		"fatalw": "fatalw-value",
	}
	for mode, want := range tests {
		mode, want := mode, want
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestLoggerFatalHelperProcess$")
			command.Env = append(os.Environ(), fatalHelperEnvironment+"="+mode)
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("helper error = %v, output = %q, want exit code 1", err, output)
			}
			if !strings.Contains(string(output), want) || !strings.Contains(string(output), "level=FATAL") {
				t.Fatalf("helper output = %q, want fatal level and %q", output, want)
			}
		})
	}
}

func TestLoggerFatalHelperProcess(t *testing.T) {
	mode := os.Getenv(fatalHelperEnvironment)
	if mode == "" {
		return
	}

	shared := &SharedState{}
	shared.config.Store(&configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			_, err := fmt.Fprintf(os.Stderr, "level=%s keyvals=%v\n", level, keyvals)
			return err
		})},
	})
	shared.custom.Store(&customState{})
	logger := &logger{shared: shared}

	switch mode {
	case "fatal":
		logger.Fatal("fatal-value")
	case "fatalf":
		logger.Fatalf("fatalf-%d", 42)
	case "fatalw":
		logger.Fatalw("event", "fatalw-value")
	default:
		t.Fatalf("unknown fatal helper mode %q", mode)
	}
	t.Fatal("fatal logger returned without exiting")
}
