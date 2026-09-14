package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"reflect"
	"sync"
	"testing"
)

func TestSharedSettingsMergeWithoutMutatingSnapshots(t *testing.T) {
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	shared.WithLevel(kratoslog.LevelWarn)
	shared.WithFilterEmpty(true)
	filters := []string{"secret"}
	shared.WithFilterKeys(filters...)
	fields := []any{"service", "orders", "scope", "ignored", "scope", "old"}
	shared.WithKV(fields...)
	shared.WithTimeFormat("2006")
	shared.WithMsgKey("message")
	before := shared.custom.Load()
	filters[0] = "changed"
	fields[1] = "changed"
	shared.WithFilterKeys("token")
	shared.WithFilterKeys()
	shared.WithKV("trace", "123", "scope", "new")
	shared.WithKV()
	got := shared.custom.Load()
	if got.level == nil || *got.level != kratoslog.LevelWarn || got.filterEmpty == nil || !*got.filterEmpty || got.timeFormat != "2006" || got.msgKey != "message" || !reflect.DeepEqual(got.filterKeys, []string{"secret", "token"}) {
		t.Fatalf("previous settings lost: %#v", got)
	}
	if !reflect.DeepEqual(got.kv, []any{"service", "orders", "scope", "new", "trace", "123"}) {
		t.Fatalf("merged fields = %v", got.kv)
	}
	if !reflect.DeepEqual(before.filterKeys, []string{"secret"}) {
		t.Fatalf("old filters mutated: %v", before.filterKeys)
	}
	if !reflect.DeepEqual(before.kv, []any{"service", "orders", "scope", "old"}) {
		t.Fatalf("old snapshot mutated: %v", before.kv)
	}
}

func TestSharedWithKVPreservesConcurrentContributions(t *testing.T) {
	const goroutines = 16
	shared := &sharedState{}
	start := make(chan struct{})
	done := make(chan struct{}, goroutines)
	var ready sync.WaitGroup
	ready.Add(goroutines)
	for index := range goroutines {
		go func() {
			ready.Done()
			<-start
			shared.WithKV(string(rune('A'+index)), index)
			done <- struct{}{}
		}()
	}
	ready.Wait()
	close(start)
	for range goroutines {
		<-done
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

func TestGlobalCustomUpdateWarnsAndPreservesState(t *testing.T) {
	previousState := processState.custom.Load()
	previousLogger := kratoslog.GetLogger()
	t.Cleanup(func() {
		processState.custom.Store(previousState)
		kratoslog.SetLogger(previousLogger)
	})
	processState.custom.Store(&customState{})
	WithKV("existing", "value")
	WithFilterKeys("secret")
	var warnings [][]any
	kratoslog.SetLogger(loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
		if level != kratoslog.LevelWarn {
			t.Errorf("level = %v, want warn", level)
		}
		warnings = append(warnings, append([]any(nil), keyvals...))
		return nil
	}))
	for _, test := range []struct {
		name  string
		field string
		run   func()
	}{
		{"level below range", "level", func() { WithLevel(-128) }},
		{"level above range", "level", func() { WithLevel(127) }},
		{"partial filters", "filter_keys", func() { WithFilterKeys("valid", " ") }},
		{"duplicate filters", "filter_keys", func() { WithFilterKeys("valid", "secret") }},
		{"odd fields", "kv", func() { WithKV("key") }},
		{"non-string key", "kv", func() { WithKV(1, "value") }},
		{"empty key", "kv", func() { WithKV("", "value") }},
		{"blank key", "kv", func() { WithKV(" ", "value") }},
		{"partial fields", "kv", func() { WithKV("existing", "changed", "", "invalid") }},
		{"time format", "time_format", func() { WithTimeFormat(" ") }},
		{"message key", "msg_key", func() { WithMsgKey(" ") }},
		{"reserved message key", "msg_key", func() { WithMsgKey("module") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			warnings = nil
			before := processState.custom.Load()
			test.run()
			if processState.custom.Load() != before {
				t.Fatal("failed update published state")
			}
			if len(warnings) != 1 {
				t.Fatalf("warnings = %v, want one", warnings)
			}
			fields := make(map[string]any)
			for i := 0; i < len(warnings[0]); i += 2 {
				fields[warnings[0][i].(string)] = warnings[0][i+1]
			}
			if fields["module"] != "log" {
				t.Fatalf("missing log module: %v", fields)
			}
			if fields["state"] != test.field {
				t.Fatalf("state = %v, want %s", fields["state"], test.field)
			}
			if err, ok := fields["error"].(error); !ok || err == nil || err.Error() == "" {
				t.Fatalf("missing failure reason: %v", fields)
			}
		})
	}
	warnings = nil
	WithTimeFormat("2006")
	if len(warnings) != 0 || processState.custom.Load().timeFormat != "2006" {
		t.Fatal("valid update should apply without warnings")
	}
}
