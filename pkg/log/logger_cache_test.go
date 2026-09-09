package log

import (
	"os"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

type loggerFunc func(kratoslog.Level, ...any) error

func (f loggerFunc) Log(level kratoslog.Level, keyvals ...any) error {
	return f(level, keyvals...)
}

func TestBuildCacheDoesNotPublishStaleVersions(t *testing.T) {
	custom := &customState{version: 1}
	oldConfig := &configState{
		version: 1,
		output:  &outputLogger{output: loggerFunc(func(kratoslog.Level, ...any) error { return nil })},
	}
	currentConfig := &configState{
		version: 2,
		output:  &outputLogger{output: loggerFunc(func(kratoslog.Level, ...any) error { return nil })},
	}
	shared := &SharedState{}
	shared.config.Store(currentConfig)
	shared.custom.Store(custom)
	l := &logger{shared: shared}

	l.buildCache(currentConfig, custom)
	l.buildCache(oldConfig, custom)

	if got := l.cache.configVersion; got != currentConfig.version {
		t.Fatalf("cache config version = %d, want %d", got, currentConfig.version)
	}
}

func TestLogRetriesWhenPublishedGenerationCloses(t *testing.T) {
	shared := &SharedState{}
	custom := &customState{}
	shared.custom.Store(custom)

	newWrites := 0
	var writtenKeyvals []any
	newConfig := &configState{
		version: 1,
		level:   kratoslog.LevelDebug,
		msgKey:  defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(_ kratoslog.Level, keyvals ...any) error {
			newWrites++
			writtenKeyvals = append([]any(nil), keyvals...)
			return nil
		})},
	}
	oldConfig := &configState{
		level:  kratoslog.LevelDebug,
		msgKey: defaultMsgKey,
		output: &outputLogger{output: loggerFunc(func(kratoslog.Level, ...any) error {
			shared.config.Store(newConfig)
			return os.ErrClosed
		})},
	}
	shared.config.Store(oldConfig)

	l := &logger{shared: shared}
	if err := l.log(kratoslog.LevelInfo, true, "value"); err != nil {
		t.Fatalf("Log() error = %v, want nil after generation retry", err)
	}
	if newWrites != 1 {
		t.Fatalf("new generation writes = %d, want 1", newWrites)
	}
	msgKeys := 0
	for i := 0; i+1 < len(writtenKeyvals); i += 2 {
		if writtenKeyvals[i] == defaultMsgKey {
			msgKeys++
		}
	}
	if msgKeys != 1 {
		t.Fatalf("message key count = %d, want 1; keyvals = %#v", msgKeys, writtenKeyvals)
	}
	if len(writtenKeyvals)%2 != 0 || writtenKeyvals[len(writtenKeyvals)-2] != defaultMsgKey || writtenKeyvals[len(writtenKeyvals)-1] != "value" {
		t.Fatalf("message pair after retry = %#v, want %q, %q", writtenKeyvals, defaultMsgKey, "value")
	}
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
	shared := &SharedState{}
	shared.config.Store(config)
	shared.custom.Store(custom)
	l := &logger{shared: shared, kv: []any{"request.id", "derived"}}

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
