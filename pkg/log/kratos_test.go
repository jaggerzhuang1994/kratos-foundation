package log

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestKratosConfigErrorsDoNotExposeSource(t *testing.T) {
	var records []string
	restore := SetLogger(loggerFunc(func(_ kratoslog.Level, fields ...any) error {
		records = append(records, fmt.Sprint(fields...))
		return nil
	}))
	defer restore()
	// 使用官方默认 decoder 触发真实错误，防止上游错误格式变化绕过桥接保护。
	for name, content := range map[string]string{"invalid.json": `{"password":"s3cr3t",invalid}`, "invalid.yaml": `s3cr3t`} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		config := kratosconfig.New(kratosconfig.WithSource(file.NewSource(path)))
		if err := config.Load(); err == nil {
			t.Fatal("invalid configuration was accepted")
		}
		if err := config.Close(); err != nil {
			t.Fatal(err)
		}
	}
	kratoslog.Errorf("Failed to config merge error: %s key: private value: %s", "s3cr3t", "s3cr3t")
	kratoslog.Errorf("failed to merge next config: %s", "s3cr3t")
	kratoslog.Info("ordinary SDK message")
	result := strings.Join(records, "\n")
	if strings.Contains(result, "s3cr3t") || !strings.Contains(result, "raw configuration error omitted") || !strings.Contains(result, "ordinary SDK message") {
		t.Fatalf("unexpected SDK records: %s", result)
	}
}

// 未装配 Wire Logger 的进程只使用标准输出，不能因全局日志打开 env 指定的文件。
func TestGlobalFallbackUsesSharedSettingsWithoutOpeningFiles(t *testing.T) {
	const mode = "KRATOS_LOG_FALLBACK_TEST"
	if os.Getenv(mode) == "1" {
		RegisterFields("fallback_field", "shared")
		if err := ApplyRuntimeConfig(&RuntimeConfig{}); err != nil {
			t.Fatal(err)
		}
		Infof("fallback-message")
		return
	}
	path := filepath.Join(t.TempDir(), "must-not-open.log")
	command := exec.Command(os.Args[0], "-test.run=^TestGlobalFallbackUsesSharedSettingsWithoutOpeningFiles$")
	command.Env = append(os.Environ(), mode+"=1", EnvFilePath+"="+path, EnvFileEnable+"=true")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback process: %v: %s", err, output)
	}
	for _, want := range []string{"module=unknown", "fallback_field=shared", "fallback-message", "caller=log/kratos_test.go:"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("fallback lacks %s: %s", want, output)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("fallback created file or stat failed: %v", err)
	}
}

func TestGlobalEntrypointsKeepModuleOwnership(t *testing.T) {
	previous := GetLogger()
	t.Cleanup(func() { SetLogger(previous) })
	var records [][]any
	sink := loggerFunc(func(_ kratoslog.Level, fields ...any) error {
		records = append(records, append([]any(nil), fields...))
		return nil
	})
	SetLogger(sink)
	Info("foundation")
	Infow("module", "config", "msg", "configured")
	Context(context.Background()).Warnw("module", "config/file", "msg", "context")
	kratoslog.Info("sdk")
	// 恢复捕获的完整适配器时不应叠加第二层，也不污染 Foundation 入口。
	SetLogger(kratoslog.GetLogger())
	Info("restored")
	for i, want := range []string{"unknown", "config", "config/file", "kratos", "unknown"} {
		count := 0
		for j := 0; j+1 < len(records[i]); j += 2 {
			if records[i][j] == "module" {
				count++
				if records[i][j+1] != want {
					t.Fatalf("record %d: %v", i, records[i])
				}
			}
		}
		if count != 1 {
			t.Fatalf("record %d module count %d: %v", i, count, records[i])
		}
	}
}

func TestGlobalFoundationContextPreservesFieldsWithoutChangingModule(t *testing.T) {
	previous := GetLogger()
	t.Cleanup(func() { SetLogger(previous) })
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	var fields []any
	base := &logger{shared: shared, config: &configState{
		level: kratoslog.LevelInfo, msgKey: defaultMsgKey,
		output: &outputLogger{preparedOutput: &preparedOutput{output: loggerFunc(func(_ kratoslog.Level, values ...any) error {
			fields = append([]any(nil), values...)
			return nil
		})}},
	}}
	SetLogger(base.WithModule("orders"))
	ctx := WithKv(context.Background(), "module", "request", "request.id", "r1")
	Context(ctx).Infow("module", "call", "msg", "event")
	values := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		values[fields[i].(string)] = fields[i+1]
	}
	if values["module"] != "orders" || values["request.id"] != "r1" {
		t.Fatal(values)
	}
	kratoslog.Info("sdk")
	if !containsLogValue(fields, "kratos") {
		t.Fatalf("SDK module did not override input module: %v", fields)
	}
	Infow("msg", "foundation")
	if !containsLogValue(fields, "orders") {
		t.Fatalf("SDK contaminated input module: %v", fields)
	}
}

func TestGlobalMessagesUseCustomMessageKey(t *testing.T) {
	previousLogger, previousState := GetLogger(), processState.custom.Load()
	t.Cleanup(func() { SetLogger(previousLogger); processState.custom.Store(previousState) })
	for _, kind := range []string{"foundation", "external"} {
		t.Run(kind, func(t *testing.T) {
			processState.custom.Store(&customState{})
			var record []any
			sink := loggerFunc(func(_ kratoslog.Level, fields ...any) error { record = append([]any(nil), fields...); return nil })
			var target kratoslog.Logger = sink
			if kind == "foundation" {
				target = &logger{shared: processState, config: &configState{output: sink, level: kratoslog.LevelDebug, msgKey: "message"}}
			}
			SetLogger(target)
			wantKey := "msg"
			if kind == "foundation" {
				wantKey = "message"
			}
			for _, emit := range []func(){
				func() { Debug("event") }, func() { Debugf("%s", "event") },
				func() { Info("event") }, func() { Infof("%s", "event") },
				func() { Warn("event") }, func() { Warnf("%s", "event") },
				func() { Error("event") }, func() { Errorf("%s", "event") },
				func() { Context(context.Background()).Info("event") },
			} {
				emit()
				fields := map[string]any{}
				for i := 0; i+1 < len(record); i += 2 {
					fields[record[i].(string)] = record[i+1]
				}
				if fields[wantKey] != "event" {
					t.Fatalf("message key ignored: %v", fields)
				}
			}
			view := WithModule("config/file").With("files", []string{"app.yaml"})

			view.Info("Matched configuration files")
			fields := map[string]any{}
			for i := 0; i+1 < len(record); i += 2 {
				fields[record[i].(string)] = record[i+1]
			}
			if fields[wantKey] != "Matched configuration files" || fields["module"] != "config/file" || fields["files"] == nil {
				t.Fatal(fields)
			}
			// 键值入口保持调用者给定的字段，不猜测哪个字段是消息。
			Infow("msg", "explicit field")
			if !containsLogValue(record, "msg") {
				t.Fatal(record)
			}
		})
	}
}

func TestGlobalEntrypointsReportCallSite(t *testing.T) {
	previous := GetLogger()
	t.Cleanup(func() { SetLogger(previous) })
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	var record []any
	SetLogger(&logger{shared: shared, config: &configState{
		level: kratoslog.LevelDebug, msgKey: defaultMsgKey,
		output: loggerFunc(func(_ kratoslog.Level, fields ...any) error {
			record = append([]any(nil), fields...)
			return nil
		}),
	}})
	// 从实际输出核对调用行，覆盖新全局包装层和 Kratos 桥接层。
	for name, emit := range map[string]func() int{
		"Infof": func() int {
			_, _, line, _ := runtime.Caller(0)
			Infof("event %d", 1)
			return line + 1
		},
		"Infow": func() int {
			_, _, line, _ := runtime.Caller(0)
			Infow("msg", "event")
			return line + 1
		},
		"Context": func() int {
			_, _, line, _ := runtime.Caller(0)
			Context(context.Background()).Info("event")
			return line + 1
		},
		"WithModule": func() int {
			_, _, line, _ := runtime.Caller(0)
			WithModule("config/file").With("file", "a.yaml").Info("event")
			return line + 1
		},
		"Kratos": func() int {
			_, _, line, _ := runtime.Caller(0)
			kratoslog.Info("event")
			return line + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			want := fmt.Sprintf("log/kratos_test.go:%d", emit())
			for i := 0; i+1 < len(record); i += 2 {
				if record[i] == "caller" && record[i+1] == want {
					return
				}
			}
			t.Fatalf("caller not %s: %v", want, record)
		})
	}
}

func TestGlobalBridgeConcurrentSwitchAndConditionalRestore(t *testing.T) {
	original := GetLogger()
	t.Cleanup(func() { SetLogger(original) })
	proxy := kratoslog.GetLogger()
	first := kratoslog.NewStdLogger(io.Discard)
	second := kratoslog.NewStdLogger(io.Discard)
	restoreFirst := SetLogger(first)
	restoreSecond := SetLogger(second)
	restoreFirst()
	if GetLogger() != second {
		t.Fatal("old restore overwrote current binding")
	}
	restoreSecond()
	restoreSecond()
	if GetLogger() != first {
		t.Fatal("restore did not preserve preceding target")
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for range 1000 {
			kratoslog.Info("concurrent log")
		}
	}()
	go func() {
		defer workers.Done()
		for range 1000 {
			restore := SetLogger(second)
			restore()
		}
	}()
	workers.Wait()
	if kratoslog.GetLogger() != proxy {
		t.Fatal("SDK proxy was replaced")
	}
}
