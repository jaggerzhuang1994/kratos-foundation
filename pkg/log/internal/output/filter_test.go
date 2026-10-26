package output

import (
	"errors"
	"reflect"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestNewFilter(t *testing.T) {
	t.Parallel()

	recorder := new(recordingLogger)
	logger := NewFilter(recorder, true, map[string]struct{}{
		"password": {},
		"secret.*": {},
	})
	wantErr := errors.New("write failed")
	recorder.err = wantErr
	err := logger.Log(
		kratoslog.LevelInfo,
		"keep", "value",
		"password", "hidden",
		"secret.token", "hidden",
		"empty", "",
		"nil", nil,
		"odd",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Log() error = %v, want %v", err, wantErr)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("underlying call count = %d, want 1", len(recorder.calls))
	}
	want := []any{"keep", "value", "odd"}
	if got := recorder.calls[0].keyvals; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered keyvals = %#v, want %#v", got, want)
	}
}

func TestNewLevelFilter(t *testing.T) {
	t.Parallel()

	recorder := new(recordingLogger)
	logger := NewLevelFilter(recorder, kratoslog.LevelWarn)
	if err := logger.Log(kratoslog.LevelInfo, "message", "ignored"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(kratoslog.LevelWarn, "message", "kept"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].level != kratoslog.LevelWarn {
		t.Fatalf("underlying calls = %#v, want one warn call", recorder.calls)
	}
}

func TestFilterKeysSetReturnsIndependentMembershipSnapshot(t *testing.T) {
	t.Parallel()

	keys := []string{"password", "secret.*", "password"}
	got := FilterKeysSet(keys)
	keys[0] = "changed"

	for _, key := range []string{"password", "secret.*"} {
		if _, ok := got[key]; !ok {
			t.Errorf("FilterKeysSet() lacks %q: %#v", key, got)
		}
	}
	if _, ok := got["changed"]; ok {
		t.Fatalf("FilterKeysSet() retained alias to input: %#v", got)
	}
	if len(got) != 2 {
		t.Fatalf("FilterKeysSet() length = %d, want 2 unique keys", len(got))
	}
}

func TestFilterPreservesModule(t *testing.T) {
	for _, key := range []string{"module", "mod*", "*"} {
		t.Run(key, func(t *testing.T) {
			recorder := new(recordingLogger)
			if err := NewFilter(recorder, true, FilterKeysSet([]string{key})).Log(kratoslog.LevelInfo, "module", "orders"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.calls[0].keyvals, []any{"module", "orders"}) {
				t.Fatal(recorder.calls)
			}
		})
	}
}

type logCall struct {
	level   kratoslog.Level
	keyvals []any
}

type recordingLogger struct {
	calls []logCall
	err   error
}

func (l *recordingLogger) Log(level kratoslog.Level, keyvals ...any) error {
	l.calls = append(l.calls, logCall{
		level:   level,
		keyvals: append([]any(nil), keyvals...),
	})
	return l.err
}

type discardLogger struct{}

func (discardLogger) Log(kratoslog.Level, ...any) error { return nil }

func BenchmarkFilterLogger(b *testing.B) {
	logger := NewFilter(discardLogger{}, true, map[string]struct{}{
		"password": {},
		"secret.*": {},
	})
	keyvals := []any{
		"request.id", "request-1",
		"password", "hidden",
		"secret.token", "hidden",
		"empty", "",
		"status", 200,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := logger.Log(kratoslog.LevelInfo, keyvals...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStackLogger(b *testing.B) {
	logger := NewStack(discardLogger{}, discardLogger{})
	keyvals := []any{"message", "benchmark", "request.id", "request-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := logger.Log(kratoslog.LevelInfo, keyvals...); err != nil {
			b.Fatal(err)
		}
	}
}
