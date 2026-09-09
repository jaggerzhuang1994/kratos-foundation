package log

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestSharedStateConcurrentUpdatePreservesEveryWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	config := Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        OutputConfig{Disable: true},
		File: FileConfig{
			OutputConfig: OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     RotatingConfig{Disable: true},
		},
	}
	shared, cleanup, err := NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	const writers, entries = 4, 100
	start := make(chan struct{})
	results := make(chan error, writers+1)
	for writer := range writers {
		logger := NewLogger(shared)
		go func() {
			<-start
			for entry := range entries {
				if err := logger.Log(kratoslog.LevelInfo, "entry", fmt.Sprintf("%d:%d", writer, entry)); err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	go func() {
		<-start
		for range 25 {
			if err := shared.Update(config); err != nil {
				results <- err
				return
			}
		}
		results <- nil
	}()
	close(start)
	for range writers + 1 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	cleanup()
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for writer := range writers {
		for entry := range entries {
			want := fmt.Sprintf("entry=%d:%d\n", writer, entry)
			if count := strings.Count(string(written), want); count != 1 {
				t.Errorf("entry %d:%d appeared %d times, want once", writer, entry, count)
			}
		}
	}
}

func TestSharedWithPreservesPreviousSettings(t *testing.T) {
	shared := &SharedState{}
	shared.custom.Store(&customState{})
	if err := shared.WithLevel(kratoslog.LevelWarn); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterEmpty(true); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterKeys("secret"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV("service", "orders", "scope", "old"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithCallerDepth(8); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithTimeFormat("2006"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithMsgKey("message"); err != nil {
		t.Fatal(err)
	}
	before := shared.custom.Load()
	if err := shared.WithKV("trace", "123", "scope", "new"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV(); err != nil {
		t.Fatal(err)
	}
	got := shared.custom.Load()
	if got.level == nil || *got.level != kratoslog.LevelWarn || got.filterEmpty == nil || !*got.filterEmpty || got.callerDepth != 8 || got.timeFormat != "2006" || got.msgKey != "message" || !reflect.DeepEqual(got.filterKeys, []string{"secret"}) {
		t.Fatalf("previous settings lost: %#v", got)
	}
	if !reflect.DeepEqual(got.kv, []any{"service", "orders", "scope", "new", "trace", "123"}) {
		t.Fatalf("merged fields = %v", got.kv)
	}
	if !reflect.DeepEqual(before.kv, []any{"service", "orders", "scope", "old"}) {
		t.Fatalf("old snapshot mutated: %v", before.kv)
	}
}

func TestSharedWithUpdatesSettingsAndRejectsDuplicateFiltersAtomically(t *testing.T) {
	shared := &SharedState{}
	if err := shared.WithFilterKeys("secret"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV("key", "old"); err != nil {
		t.Fatal(err)
	}
	before := shared.custom.Load()
	if err := shared.WithFilterKeys("secret"); err == nil {
		t.Fatal("duplicate filter accepted")
	}
	if shared.custom.Load() != before {
		t.Fatal("failed merge published partial state")
	}
	if err := shared.WithLevel(kratoslog.LevelError); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterEmpty(false); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithCallerDepth(9); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithTimeFormat("15:04"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithMsgKey("text"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithFilterKeys("token"); err != nil {
		t.Fatal(err)
	}
	got := shared.custom.Load()
	if *got.level != kratoslog.LevelError || *got.filterEmpty || got.callerDepth != 9 || got.timeFormat != "15:04" || got.msgKey != "text" || !reflect.DeepEqual(got.filterKeys, []string{"secret", "token"}) {
		t.Fatalf("settings not applied: %#v", got)
	}
	if !reflect.DeepEqual(before.filterKeys, []string{"secret"}) {
		t.Fatal("old filters mutated")
	}
}

func TestSharedMethodsRejectInvalidValuesWithoutPublishing(t *testing.T) {
	shared := &SharedState{}
	tests := []struct {
		name string
		run  func() error
	}{
		{"caller depth", func() error { return shared.WithCallerDepth(0) }},
		{"time format", func() error { return shared.WithTimeFormat(" ") }},
		{"message key", func() error { return shared.WithMsgKey(" ") }},
		{"partial filters", func() error { return shared.WithFilterKeys("valid", " ") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := shared.custom.Load()
			if err := tt.run(); err == nil {
				t.Fatal("invalid setting accepted")
			}
			if shared.custom.Load() != before {
				t.Fatal("failed method published state")
			}
		})
	}
}

func TestSharedWithKVRejectsInvalidFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		kv   []any
	}{
		{"odd fields", []any{"key"}},
		{"non-string key", []any{1, "value"}},
		{"empty key", []any{"", "value"}},
		{"blank key", []any{" \t ", "value"}},
		{"invalid suffix", []any{"valid", "value", "", "invalid"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			shared := &SharedState{}
			if err := shared.WithKV("existing", "value"); err != nil {
				t.Fatal(err)
			}
			before := shared.custom.Load()
			if err := shared.WithKV(tt.kv...); err == nil {
				t.Fatal("invalid fields were accepted")
			}
			if shared.custom.Load() != before {
				t.Fatal("invalid contribution changed shared state")
			}
		})
	}
}

func TestSharedWithKVPreservesConcurrentContributions(t *testing.T) {
	const goroutines = 16
	shared := &SharedState{}
	start := make(chan struct{})
	errors := make(chan error, goroutines)
	var ready sync.WaitGroup
	ready.Add(goroutines)
	for index := range goroutines {
		go func() {
			ready.Done()
			<-start
			errors <- shared.WithKV(string(rune('A'+index)), index)
		}()
	}
	ready.Wait()
	close(start)
	for range goroutines {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	state := shared.custom.Load()
	if state == nil || len(state.kv) != goroutines*2 {
		t.Fatalf("contribution state = %#v", state)
	}
	got := make(map[string]any)
	for i := 0; i < len(state.kv); i += 2 {
		got[state.kv[i].(string)] = state.kv[i+1]
	}
	for index := range goroutines {
		if got[string(rune('A'+index))] != index {
			t.Fatalf("contribution %d lost: %v", index, got)
		}
	}
}

func TestSharedWithKVMergesFieldsAndCopiesInput(t *testing.T) {
	shared := &SharedState{}
	if err := shared.WithKV(); err != nil {
		t.Fatal(err)
	}
	fields := []any{"key", "first", "key", "second"}
	if err := shared.WithKV(fields...); err != nil {
		t.Fatal(err)
	}
	before := shared.custom.Load()
	fields[1] = "changed"
	if err := shared.WithKV("key", "third", "new", "value"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.kv, []any{"key", "second"}) {
		t.Fatalf("old snapshot changed: %v", before.kv)
	}
	want := []any{"key", "third", "new", "value"}
	if got := shared.custom.Load().kv; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestKVContributionAccumulatesAndYieldsToExplicitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	shared, cleanup, err := NewSharedState(contributionLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	if err := shared.WithKV(
		"scope", "component-old",
		"component.old", "stale",
	); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV(
		"scope", "component-new",
		"component.new", "current",
	); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV("scope", "explicit"); err != nil {
		t.Fatal(err)
	}
	logger := NewLogger(shared)
	if err := logger.Log(kratoslog.LevelInfo, "event", "with-custom"); err != nil {
		t.Fatal(err)
	}
	if err := shared.WithKV(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(kratoslog.LevelInfo, "event", "after-empty-apply"); err != nil {
		t.Fatal(err)
	}
	cleanup()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2: %s", len(lines), written)
	}
	if !strings.Contains(lines[0], "scope=explicit") ||
		!strings.Contains(lines[0], "component.new=current") {
		t.Fatalf("explicit custom did not win over component contribution: %s", lines[0])
	}
	if !strings.Contains(lines[1], "scope=explicit") ||
		!strings.Contains(lines[1], "component.new=current") {
		t.Fatalf("empty WithKV changed custom fields: %s", lines[1])
	}
	for _, line := range lines {
		if !strings.Contains(line, "component.old=stale") {
			t.Fatalf("earlier contribution was lost: %s", line)
		}
	}
}

func contributionLogConfig(path string) Config {
	return Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: false,
		TimeFormat:  time.RFC3339,
		Std: OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: FileConfig{
			OutputConfig: OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     RotatingConfig{Disable: true},
		},
	}
}

func TestSharedStateUpdateAndCleanupLifecycle(t *testing.T) {
	config := Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: FileConfig{OutputConfig: OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	}
	shared, cleanup, err := NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	config.FilterKeys = []string{"secret"}
	if err := shared.Update(config); err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()
	if err := shared.Update(config); !errors.Is(err, errSharedStateReleased) {
		t.Fatalf("Update after cleanup error = %v", err)
	}
	if err := NewLogger(shared).Log(kratoslog.LevelInfo, "event", "closed"); !errors.Is(err, errSharedStateReleased) {
		t.Fatalf("Log after cleanup error = %v", err)
	}
}

func TestWithLevelOverridesRootLevelAndRejectsInvalidValues(t *testing.T) {
	var levels []kratoslog.Level
	shared := &SharedState{}
	shared.config.Store(&configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(level kratoslog.Level, _ ...any) error {
			levels = append(levels, level)
			return nil
		})},
	})
	shared.custom.Store(&customState{})

	if err := shared.WithLevel(kratoslog.LevelWarn); err != nil {
		t.Fatal(err)
	}
	logger := NewLogger(shared)
	if err := logger.Log(kratoslog.LevelInfo, "message", "filtered"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(kratoslog.LevelWarn, "message", "written"); err != nil {
		t.Fatal(err)
	}
	if len(levels) != 1 || levels[0] != kratoslog.LevelWarn {
		t.Fatalf("written levels = %v, want only warn", levels)
	}

	for _, invalid := range []kratoslog.Level{-128, 127} {
		if err := (&SharedState{}).WithLevel(invalid); err == nil {
			t.Errorf("WithLevel(%d) accepted invalid level", invalid)
		}
	}
}
