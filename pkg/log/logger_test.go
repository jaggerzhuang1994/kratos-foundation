package log

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/pprof"
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

type formattingProbe struct{ calls int }

func (p *formattingProbe) String() string {
	p.calls++
	return "payload"
}

func TestFilteredLogHelpersDoNotFormatMessages(t *testing.T) {
	methods := []struct {
		name string
		log  func(Logger, any)
	}{
		{"Debug", func(l Logger, value any) { l.Debug(value) }},
		{"Debugf", func(l Logger, value any) { l.Debugf("%s", value) }},
		{"Info", func(l Logger, value any) { l.Info(value) }},
		{"Infof", func(l Logger, value any) { l.Infof("%s", value) }},
		{"Warn", func(l Logger, value any) { l.Warn(value) }},
		{"Warnf", func(l Logger, value any) { l.Warnf("%s", value) }},
		{"Error", func(l Logger, value any) { l.Error(value) }},
		{"Errorf", func(l Logger, value any) { l.Errorf("%s", value) }},
	}
	for _, policy := range []string{"root", "shared", "module", "disabled"} {
		t.Run(policy, func(t *testing.T) {
			shared := &sharedState{}
			shared.custom.Store(&customState{})
			base := &logger{shared: shared, config: &configState{level: kratoslog.LevelDebug}}
			var l Logger = base
			switch policy {
			case "root":
				base.config.level = kratoslog.LevelFatal
			case "shared":
				shared.WithLevel(kratoslog.LevelFatal)
			case "module", "disabled":
				var err error
				level := "fatal"
				if policy == "disabled" {
					level = "debug"
				}
				l, err = base.WithModuleConfig("test", testModuleConfig{level: level, disable: policy == "disabled"})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, method := range methods {
				t.Run(method.name, func(t *testing.T) {
					probe := &formattingProbe{}
					method.log(l, probe)
					if probe.calls != 0 {
						t.Fatalf("filtered message formatted %d times", probe.calls)
					}
				})
			}
		})
	}
}

func BenchmarkLoggerFilteredMessages(b *testing.B) {
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	l := &logger{shared: shared, config: &configState{level: kratoslog.LevelInfo}}
	payload := map[string]any{"id": 42, "status": "ready", "items": []int{1, 2, 3, 4, 5}}
	b.Run("Debugf", func(b *testing.B) {
		for b.Loop() {
			l.Debugf("payload=%+v", payload)
		}
	})
	b.Run("Debugw", func(b *testing.B) {
		for b.Loop() {
			l.Debugw("payload", payload)
		}
	})
}

func TestLoggerHelperMethodsEmitExpectedLevelsAndPayloads(t *testing.T) {
	var records []capturedLogRecord
	shared := &sharedState{}
	base := &configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			records = append(records, capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)})
			return nil
		})},
	}
	shared.custom.Store(&customState{})
	logger := &logger{shared: shared, config: base}

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
	shared := &sharedState{}
	base := &configState{
		level:       kratoslog.LevelInfo,
		filterEmpty: false,
		msgKey:      defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, keyvals ...any) error {
			mu.Lock()
			defer mu.Unlock()
			written = append([]any(nil), keyvals...)
			return nil
		})},
	}
	shared.custom.Store(&customState{})
	shared.WithFilterEmpty(true)
	shared.WithFilterKeys("global.secret")
	shared.WithKV("global", "value")
	shared.WithTimeFormat("2006")
	shared.WithMsgKey("message")
	ctx := WithKv(context.Background(), "request.id", "r1")
	contextCopy := kvFromCtx(ctx)
	contextCopy[1] = "mutated"
	if kvFromCtx(ctx)[1] != "r1" {
		t.Fatal("kvFromCtx returned aliased state")
	}
	derived, err := (&logger{shared: shared, config: base}).WithModuleConfig("orders", testModuleConfig{
		level:      "debug",
		filterKeys: []string{"module.secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	derived = derived.
		WithContext(ctx).
		WithCallerDepth(2).
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
	// 派生级别优先于共享级别，提前过滤不能误丢允许输出的消息。
	shared.WithLevel(kratoslog.LevelFatal)
	probe := &formattingProbe{}
	derived.Debugf("%s", probe)
	if probe.calls != 1 || !containsLogValue(written, "payload") {
		t.Fatalf("module debug override was filtered: calls=%d, output=%v", probe.calls, written)
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
	config := envConfig{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: outputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: fileConfig{
			outputConfig: outputConfig{
				Disable: true,
				Level:   kratoslog.LevelInfo,
			},
		},
	}
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	rootLogger, cleanup, err := newLogger(shared, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if rootLogger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

func TestPublicLoggerAppliesContextOverridesAndRuntimeUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundation.log")
	config := envConfig{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        outputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: fileConfig{
			outputConfig: outputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     rotatingConfig{Disable: true},
		},
	}
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	rootLogger, cleanup, err := newLogger(shared, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	ctx := WithKv(context.Background(), "request.id", "r1")
	ctx = WithKv(ctx, "tenant", "acme")
	contextFields := kvFromCtx(ctx)
	contextFields[1] = "mutated"
	if got := kvFromCtx(ctx); len(got) != 4 || got[1] != "r1" || got[3] != "acme" {
		t.Fatalf("context fields were not appended and copied: %#v", got)
	}
	shared.WithLevel(kratoslog.LevelDebug)
	shared.WithFilterEmpty(true)
	shared.WithFilterKeys("secret")
	shared.WithKV("global", "value")
	shared.WithTimeFormat("2006-01-02")
	shared.WithMsgKey("message")
	logger := rootLogger.
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

	shared.WithLevel(kratoslog.LevelError)
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
		"global-fatal":    "fatal-value",
		"global-fatalf":   "fatalf-42",
		"fatal":           "fatal-value",
		"fatalf":          "fatalf-42",
		"fatalw":          "fatalw-value",
		"disabled-fatal":  "",
		"disabled-fatalf": "",
		"disabled-fatalw": "",
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
			if want == "" {
				if len(output) != 0 {
					t.Fatalf("disabled fatal output = %q", output)
				}
				return
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

	shared := &sharedState{}
	base := &configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			_, err := fmt.Fprintf(os.Stderr, "level=%s keyvals=%v\n", level, keyvals)
			return err
		})},
	}
	shared.custom.Store(&customState{})
	logger := &logger{shared: shared, config: base}

	if strings.HasPrefix(mode, "disabled-") {
		logger.disabled = true
		mode = strings.TrimPrefix(mode, "disabled-")
	}
	switch mode {
	case "global-fatal":
		SetLogger(logger)
		Fatal("fatal-value")
	case "global-fatalf":
		SetLogger(logger)
		Fatalf("fatalf-%d", 42)
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

// setSharedLogEnvironment 通过正式 env 初始化入口准备全局日志测试，不绕过资源生命周期。
func setSharedLogEnvironment(t *testing.T, path string) {
	t.Helper()
	clearEnvironment(t, logEnvironmentKeys...)
	t.Setenv(EnvLevel, "info")
	t.Setenv(EnvStdDisable, "true")
	t.Setenv(EnvFileDisable, "false")
	t.Setenv(EnvFilePath, path)
	t.Setenv(EnvFileRotatingDisable, "true")
}

func TestLoggerDeduplicatesCompleteKeyvalsBeforeOutput(t *testing.T) {
	var writtenKeyvals []any
	config := &configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, keyvals ...any) error {
			writtenKeyvals = append([]any(nil), keyvals...)
			return nil
		})},
	}
	custom := &customState{kv: []any{"request.id", "custom"}}
	shared := &sharedState{}
	shared.custom.Store(custom)
	l := &logger{shared: shared, config: config, kv: []any{"request.id", "derived"}}

	if err := l.Log(kratoslog.LevelInfo, "request.id", "call"); err != nil {
		t.Fatal(err)
	}

	values := make([]any, 0, 1)
	for index := 0; index+1 < len(writtenKeyvals); index += 2 {
		if writtenKeyvals[index] == "request.id" {
			values = append(values, writtenKeyvals[index+1])
		}
	}
	if len(values) != 1 || values[0] != "call" {
		t.Fatalf("request.id values = %#v, want []any{%q}; keyvals = %#v", values, "call", writtenKeyvals)
	}
}

// loggerFunc 供同包测试观察最终输出，不持有外部资源。
type loggerFunc func(kratoslog.Level, ...any) error

func (f loggerFunc) Log(level kratoslog.Level, keyvals ...any) error { return f(level, keyvals...) }

// TestLoggerInstancesOwnOutputsAndShareSettings 验证 Wire 实例资源隔离且进程设置统一生效。
func TestLoggerInstancesOwnOutputsAndShareSettings(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	processState.custom.Store(&customState{})
	firstPath := filepath.Join(t.TempDir(), "first.log")
	setSharedLogEnvironment(t, firstPath)
	first, releaseFirst, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	secondPath := filepath.Join(t.TempDir(), "second.log")
	t.Setenv(EnvFilePath, secondPath)
	second, releaseSecond, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	derived := first.WithModule("worker")
	WithKV("shared_field", "before")
	WithFilterEmpty(true)
	first.Infow("msg", "first", "empty_field", "")
	second.Infow("msg", "second", "empty_field", "")
	releaseFirst()
	releaseFirst()
	for _, closed := range []Logger{first, derived} {
		if err := closed.Log(kratoslog.LevelInfo, "msg", "after-close"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed instance write = %v, want os.ErrClosed", err)
		}
	}
	WithKV("shared_field", "after")
	second.Info("still-open")
	WithLevel(kratoslog.LevelError)
	second.Info("filtered")
	releaseSecond()
	for path, want := range map[string][]string{
		firstPath:  {"shared_field=before", "msg=first"},
		secondPath: {"shared_field=before", "shared_field=after", "msg=still-open"},
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range want {
			if !strings.Contains(string(data), value) {
				t.Fatalf("%s lacks %s: %s", path, value, data)
			}
		}
		if strings.Contains(string(data), "after-close") || strings.Contains(string(data), "filtered") || strings.Contains(string(data), "empty_field") {
			t.Fatalf("unexpected write: %s", data)
		}
	}
	if processState.custom.Load().level == nil || *processState.custom.Load().level != kratoslog.LevelError {
		t.Fatal("instance cleanup reset shared settings")
	}
	// 创建后续实例不会重新打开已关闭实例的输出，也不清除共享设置。
	t.Setenv(EnvFilePath, filepath.Join(t.TempDir(), "third.log"))
	third, cleanup, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if third.(*logger).shared != processState {
		t.Fatal("new instance lost shared state")
	}
	if err := first.Log(kratoslog.LevelError, "msg", "closed"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old output reopened: %v", err)
	}
}

func TestNewLoggerReportsEnvironmentErrors(t *testing.T) {
	t.Setenv(EnvLevel, "not-a-level")
	logger, cleanup, err := NewLogger()
	if err == nil || logger != nil || cleanup != nil {
		t.Fatal("invalid environment was accepted")
	}
}

func TestLoggerConcurrentWritesAndSharedUpdates(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	processState.custom.Store(&customState{})
	path := filepath.Join(t.TempDir(), "concurrent.log")
	setSharedLogEnvironment(t, path)
	logger, cleanup, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	const writers, entries = 4, 100
	start := make(chan struct{})
	var workers sync.WaitGroup
	for writer := range writers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for entry := range entries {
				if err := logger.Log(kratoslog.LevelInfo, "entry", fmt.Sprintf("%d:%d", writer, entry)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := range 100 {
			WithKV("revision", i)
		}
	}()
	close(start)
	workers.Wait()
	cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for writer := range writers {
		for entry := range entries {
			if count := strings.Count(string(data), fmt.Sprintf("entry=%d:%d\n", writer, entry)); count != 1 {
				t.Fatalf("entry %d:%d count=%d", writer, entry, count)
			}
		}
	}
}

func TestLoggerCleanupReleasesRotationWorkers(t *testing.T) {
	workers := func() int {
		var stacks bytes.Buffer
		if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
			t.Fatal(err)
		}
		return strings.Count(stacks.String(), ".(*Logger).millRun(")
	}
	before := workers()
	path := filepath.Join(t.TempDir(), "rotating.log")
	setSharedLogEnvironment(t, path)
	t.Setenv(EnvFileRotatingDisable, "false")
	t.Setenv(EnvFileRotatingMaxSize, "1")
	for range 3 {
		logger, cleanup, err := NewLogger()
		if err != nil {
			t.Fatal(err)
		}
		if err := logger.Log(kratoslog.LevelInfo, "msg", "rotation owner"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		cleanup()
		cleanup()
	}
	deadline := time.Now().Add(time.Second)
	for workers() > before && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if after := workers(); after > before {
		t.Fatalf("rotation workers leaked: before=%d after=%d", before, after)
	}
}

func TestBuildCacheDoesNotPublishStaleSharedSettings(t *testing.T) {
	shared := &sharedState{}
	old := &customState{version: 1}
	current := &customState{version: 2}
	shared.custom.Store(current)
	l := &logger{shared: shared, config: &configState{
		output: &outputLogger{output: loggerFunc(func(kratoslog.Level, ...any) error { return nil })},
	}}
	if !l.buildCache(current) {
		t.Fatal("current settings were rejected")
	}
	if l.buildCache(old) {
		t.Fatal("stale settings were accepted")
	}
	if l.cache.customVersion != current.version {
		t.Fatalf("cache version = %d", l.cache.customVersion)
	}
}

// TestIntegrationLoggerModulePolicies 经环境构造与真实文件输出，验证两层级别及模块隔离。
func TestIntegrationLoggerModulePolicies(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	processState.custom.Store(&customState{})
	for _, test := range []struct {
		name, fileLevel, moduleLevel string
		disabled                     bool
		wantDebug, wantInfo          bool
	}{
		{"module inherits root", "debug", "", false, false, true},
		{"module lowers root threshold", "debug", "debug", false, true, true},
		{"file threshold remains final", "error", "debug", false, false, false},
		{"module raises root threshold", "debug", "error", false, false, false},
		{"disabled module stays silent", "debug", "debug", true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "module.log")
			setSharedLogEnvironment(t, path)
			t.Setenv(EnvFileLevel, test.fileLevel)
			root, cleanup, err := NewLogger()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			module, err := root.WithModuleConfig("orders", testModuleConfig{level: test.moduleLevel, disable: test.disabled})
			if err != nil {
				t.Fatal(err)
			}
			module.Debug("debug-event")
			module.Info("info-event")
			module.Error("error-event")
			root.Error("root-still-enabled")
			cleanup()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for event, want := range map[string]bool{
				"debug-event": test.wantDebug, "info-event": test.wantInfo,
				"error-event": !test.disabled, "root-still-enabled": true,
			} {
				if got := strings.Contains(string(data), "msg="+event); got != want {
					t.Fatalf("event %q present=%t want=%t: %s", event, got, want, data)
				}
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if strings.Contains(line, "root-still-enabled") == strings.Contains(line, "module=orders") {
					t.Fatalf("module policy contaminated root or lost module: %s", line)
				}
			}
		})
	}
}

// TestIntegrationLoggerRequestFields 将固定字段、请求上下文、Valuer 和输出过滤组合到真实文件。
func TestIntegrationLoggerRequestFields(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	processState.custom.Store(&customState{})
	path := filepath.Join(t.TempDir(), "requests.log")
	setSharedLogEnvironment(t, path)
	t.Setenv(EnvFilterKeys, "root_secret")
	t.Setenv(EnvFileFilterKeys, "sink_secret")
	root, cleanup, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	type requestKey struct{}
	WithKV("request_id", "global")
	module, err := root.WithModuleConfig("orders", testModuleConfig{filterKeys: []string{"module_secret"}})
	if err != nil {
		t.Fatal(err)
	}
	module = module.With("request_id", "fixed").WithFilterKeys("local_secret")
	for _, id := range []string{"request-a", "request-b"} {
		ctx := context.WithValue(t.Context(), requestKey{}, id)
		ctx = WithKv(ctx, "request_id", "context", "request_name", id)
		requestLogger := module.WithContext(ctx).With("evaluated", kratoslog.Valuer(func(ctx context.Context) any {
			return ctx.Value(requestKey{})
		}))
		if err := requestLogger.Log(kratoslog.LevelInfo, "request_id", id,
			"root_secret", "root-private", "sink_secret", "sink-private",
			"module_secret", "module-private", "local_secret", "local-private", "empty", ""); err != nil {
			t.Fatal(err)
		}
	}
	cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines=%d want=2: %s", len(lines), data)
	}
	for i, id := range []string{"request-a", "request-b"} {
		for _, field := range []string{"request_id=" + id, "request_name=" + id, "evaluated=" + id, "module=orders"} {
			if !strings.Contains(lines[i], field) {
				t.Fatalf("missing %q: %s", field, lines[i])
			}
		}
		if strings.Count(lines[i], "request_id=") != 1 {
			t.Fatalf("request override not deduplicated: %s", lines[i])
		}
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "empty=") {
		t.Fatalf("filter chain leaked values: %s", data)
	}
}

// TestIntegrationLoggerEnvironmentSnapshot 验证环境只在构造期读取，以及释放后同路径重建的追加语义。
func TestIntegrationLoggerEnvironmentSnapshot(t *testing.T) {
	previous := processState.custom.Load()
	t.Cleanup(func() { processState.custom.Store(previous) })
	processState.custom.Store(&customState{})
	path := filepath.Join(t.TempDir(), "append.log")
	setSharedLogEnvironment(t, path)
	first, closeFirst, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeFirst)
	t.Setenv(EnvLevel, "error")
	first.Info("original-environment")
	closeFirst()
	second, closeSecond, err := NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSecond)
	second.Info("new-environment-filtered")
	second.Error("appended-after-reopen")
	if err := first.WithModule("closed").Log(kratoslog.LevelError, "msg", "old-owner"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed logger err=%v want=os.ErrClosed", err)
	}
	closeSecond()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "original-environment") || !strings.Contains(string(data), "appended-after-reopen") ||
		strings.Contains(string(data), "new-environment-filtered") || strings.Contains(string(data), "old-owner") {
		t.Fatalf("environment snapshot or append semantics broken: %s", data)
	}
}

func TestCallerAcrossLoggingEntrypoints(t *testing.T) {
	for _, entry := range []struct {
		name  string
		write func(Logger) int
	}{
		{"direct", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			l.Info("message")
			return line + 1
		}},
		{"derived", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			l.With("key", "value").WithModule("test").WithContext(context.Background()).Infof("%s", "message")
			return line + 1
		}},
		{"structured", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			l.Infow("msg", "message")
			return line + 1
		}},
		{"Log", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			_ = l.Log(kratoslog.LevelInfo, "msg", "message")
			return line + 1
		}},
		{"Helper", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			kratoslog.NewHelper(l).Info("message")
			return line + 1
		}},
		{"HelperContext", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			kratoslog.NewHelper(l).WithContext(context.Background()).Info("message")
			return line + 1
		}},
		{"With", func(l Logger) int {
			_, _, line, _ := runtime.Caller(0)
			_ = kratoslog.With(l, "key", "value").Log(kratoslog.LevelInfo, "msg", "message")
			return line + 1
		}},
		{"global", func(l Logger) int {
			kratoslog.SetLogger(l)
			_, _, line, _ := runtime.Caller(0)
			kratoslog.Info("message")
			return line + 1
		}},
		{"globalContext", func(l Logger) int {
			kratoslog.SetLogger(l)
			_, _, line, _ := runtime.Caller(0)
			kratoslog.Context(context.Background()).Info("message")
			return line + 1
		}},
		{"GetLogger", func(l Logger) int {
			kratoslog.SetLogger(l)
			_, _, line, _ := runtime.Caller(0)
			_ = kratoslog.GetLogger().Log(kratoslog.LevelInfo, "msg", "message")
			return line + 1
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			previous := kratoslog.GetLogger()
			defer kratoslog.SetLogger(previous)
			shared := &sharedState{}
			shared.custom.Store(&customState{})
			var caller any
			l := &logger{shared: shared, config: &configState{msgKey: defaultMsgKey,
				output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, kv ...any) error {
					for i := 0; i+1 < len(kv); i += 2 {
						if kv[i] == CallerKey {
							caller = kv[i+1]
						}
					}
					return nil
				})}}}
			line := entry.write(l)
			if want := fmt.Sprintf("log/logger_test.go:%d", line); caller != want {
				t.Fatalf("caller=%v, want %s", caller, want)
			}
		})
	}
}

func TestCallerDepthCountsOnlyApplicationFrames(t *testing.T) {
	for _, depth := range []int{-1, 0, 1, 2} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			shared := &sharedState{}
			shared.custom.Store(&customState{})
			var got any
			root := &logger{shared: shared, config: &configState{msgKey: defaultMsgKey, output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, kv ...any) error {
				for i := 0; i+1 < len(kv); i += 2 {
					if kv[i] == CallerKey {
						got = kv[i+1]
					}
				}
				return nil
			})}}}
			// 后一次设置覆盖前一次，其他派生操作保持选择；根 Logger 不受影响。
			l := root.WithCallerDepth(9).WithCallerDepth(depth).WithModule("test").WithContext(context.Background())
			for range 2 {
				_, _, outerLine, _ := runtime.Caller(0)
				innerLine := writeCallerThroughWrapper(l)
				wantLine := innerLine
				if depth == 2 {
					wantLine = outerLine + 1
				}
				if want := fmt.Sprintf("log/logger_test.go:%d", wantLine); got != want {
					t.Fatalf("caller=%v, want %s", got, want)
				}
			}
			_, _, line, _ := runtime.Caller(0)
			root.Info("root unchanged")
			if want := fmt.Sprintf("log/logger_test.go:%d", line+1); got != want {
				t.Fatalf("root caller=%v, want %s", got, want)
			}
		})
	}
}

func writeCallerThroughWrapper(l Logger) int {
	_, _, line, _ := runtime.Caller(0)
	kratoslog.NewHelper(l).WithContext(context.Background()).Info("message")
	return line + 1
}

func TestModuleOwnershipAndOutputFilters(t *testing.T) {
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	shared.WithKV("module", "shared")
	shared.WithFilterKeys("module")
	for _, test := range []struct{ name, module, want string }{
		{"unknown ignores context and shared", "", "unknown"},
		{"component wins", "orders", "orders"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "module.log")
			config := envConfig{Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339, File: fileConfig{Rotating: rotatingConfig{Disable: true}}}
			config.File.Path = path
			config.File.Disable = false
			config.File.FilterKeys = []string{"*"}
			config.Std.Disable = true
			root, cleanup, err := newLogger(shared, config)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			l := root.WithContext(WithKv(context.Background(), "module", "context"))
			if test.module != "" {
				l = l.With("module", "fixed-kv").WithModule(test.module)
			}
			l = l.WithFilterKeys("mod*")
			fields := []any{"msg", "event"}
			if test.module != "" {
				fields = append(fields, "module", "call")
			}
			if err := l.Log(kratoslog.LevelInfo, fields...); err != nil {
				t.Fatal(err)
			}
			cleanup()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), "module=") != 1 || !strings.Contains(string(data), "module="+test.want) {
				t.Fatalf("unexpected output: %s", data)
			}
		})
	}
}

func TestLoggerDisplaysModuleBetweenTimestampAndCaller(t *testing.T) {
	for _, module := range []string{"", "orders"} {
		t.Run(module, func(t *testing.T) {
			var buffer bytes.Buffer
			shared := &sharedState{}
			shared.custom.Store(&customState{})
			base := &logger{shared: shared, config: &configState{
				output: kratoslog.NewStdLogger(&buffer), level: kratoslog.LevelInfo, msgKey: defaultMsgKey,
			}}
			var view Logger = base
			wantModule := "unknown"
			if module != "" {
				view = base.WithModule(module)
				wantModule = module
			}
			view.With("ts", "now", "caller", "app.go:10", "a", 1, "b", 2).Info("ready")
			want := "INFO ts=now module=" + wantModule + " caller=app.go:10 a=1 b=2 msg=ready\n"
			if got := buffer.String(); got != want {
				t.Fatalf("output=%q want=%q", got, want)
			}
		})
	}
}
