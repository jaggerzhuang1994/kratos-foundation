package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"reflect"
	"sync"
	"testing"
)

func TestRegisteredFieldsPreserveSnapshots(t *testing.T) {
	shared := &sharedState{}
	fields := []any{"service", "orders"}
	shared.WithKV(fields...)
	old := shared.custom.Load()
	fields[1] = "mutated"
	shared.WithKV("service", "new", "trace", "id")
	shared.WithKV()
	if !reflect.DeepEqual(old.kv, []any{"service", "orders"}) {
		t.Fatal("old metadata mutated")
	}
	if !reflect.DeepEqual(shared.custom.Load().kv, []any{"service", "new", "trace", "id"}) {
		t.Fatal("metadata merge failed")
	}
}

// 登记元数据不得改变省略过滤规则与显式清空规则的区别。
func TestRegisteredFieldsPreserveFilterOverride(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want any
	}{
		{name: "inherit env"},
		{name: "clear env", keys: []string{}, want: "visible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shared := &sharedState{}
			if err := shared.applyRuntimeConfig(&RuntimeConfig{FilterKeys: tc.keys}); err != nil {
				t.Fatal(err)
			}
			var got any
			logger := &logger{shared: shared, config: &configState{
				filterKeys: []string{"token"},
				output: loggerFunc(func(_ kratoslog.Level, fields ...any) error {
					got = nil
					for i := 0; i+1 < len(fields); i += 2 {
						if fields[i] == "token" {
							got = fields[i+1]
						}
					}
					return nil
				}),
			}}
			logger.Infow("token", "visible")
			if got != tc.want {
				t.Fatalf("before registration token=%v, want %v", got, tc.want)
			}
			shared.WithKV("service", "orders")
			logger.Infow("token", "visible")
			if got != tc.want {
				t.Fatalf("after registration token=%v, want %v", got, tc.want)
			}
		})
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
	previousLogger := GetLogger()
	t.Cleanup(func() {
		processState.custom.Store(previousState)
		SetLogger(previousLogger)
	})
	processState.custom.Store(&customState{})
	processState.WithKV("existing", "value")
	var warnings [][]any
	SetLogger(loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
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
		{"odd fields", "kv", func() { processState.WithKV("key") }},
		{"non-string key", "kv", func() { processState.WithKV(1, "value") }},
		{"empty key", "kv", func() { processState.WithKV("", "value") }},
		{"blank key", "kv", func() { processState.WithKV(" ", "value") }},
		{"partial fields", "kv", func() { processState.WithKV("existing", "changed", "", "invalid") }},
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
	RegisterFields("registered", "yes")
	if len(warnings) != 0 {
		t.Fatal("valid metadata emitted warning")
	}
}
