package log

import (
	"context"
	"reflect"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
)

func TestDebugOnlyAppliesToEveryFieldSource(t *testing.T) {
	var records []capturedLogRecord
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	shared.WithKV("globalKey", DebugOnly("globalValue"))
	base := &logger{shared: shared, config: &configState{
		level:       kratoslog.LevelDebug,
		filterEmpty: true,
		msgKey:      defaultMsgKey,
		output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			records = append(records, capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)})
			return nil
		}),
	}}
	ctx := WithKv(context.Background(), "contextKey", DebugOnly("contextValue"))
	logger := base.WithContext(ctx).With("boundKey", DebugOnly("boundValue"))

	logger.Infow("event", "info", "callKey", DebugOnly("callValue"))
	logger.Debugw("event", "debug", "callKey", DebugOnly("callValue"))

	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	for _, key := range []string{"globalKey", "boundKey", "contextKey", "callKey"} {
		if _, ok := fieldValue(records[0].keyvals, key); ok {
			t.Errorf("info record retained %q: %#v", key, records[0].keyvals)
		}
	}
	for key, want := range map[string]any{
		"globalKey":  "globalValue",
		"boundKey":   "boundValue",
		"contextKey": "contextValue",
		"callKey":    "callValue",
	} {
		got, ok := fieldValue(records[1].keyvals, key)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("debug field %q = %#v, %v, want %#v", key, got, ok, want)
		}
	}
}

func TestDebugOnlyDefersValuerUntilDebugEvent(t *testing.T) {
	calls := 0
	value := DebugOnly(kratoslog.Valuer(func(context.Context) any {
		calls++
		return "stack"
	}))
	var records []capturedLogRecord
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	logger := (&logger{shared: shared, config: &configState{
		level:       kratoslog.LevelDebug,
		filterEmpty: true,
		msgKey:      defaultMsgKey,
		output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			records = append(records, capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)})
			return nil
		}),
	}}).With("error.stack", value)

	logger.Info("info")
	if calls != 0 {
		t.Fatalf("valuer calls after info = %d, want 0", calls)
	}
	logger.Debug("debug")
	if calls != 1 {
		t.Fatalf("valuer calls after debug = %d, want 1", calls)
	}
	if got, ok := fieldValue(records[1].keyvals, "error.stack"); !ok || got != "stack" {
		t.Fatalf("debug error.stack = %#v, %v, want stack", got, ok)
	}
}

func TestDebugOnlyAppliesToRequestDebugInfoEvent(t *testing.T) {
	calls := 0
	var record capturedLogRecord
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	shared.WithKV("globalKey", DebugOnly("globalValue"))
	base := &logger{shared: shared, config: &configState{
		level:       kratoslog.LevelInfo,
		filterEmpty: true,
		msgKey:      defaultMsgKey,
		output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			record = capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)}
			return nil
		}),
	}}
	ctx := WithKv(request.WithDebug(context.Background()),
		"contextKey", DebugOnly(kratoslog.Valuer(func(context.Context) any {
			calls++
			return "contextValue"
		})),
	)

	base.WithContext(ctx).
		With("boundKey", DebugOnly("boundValue")).
		Infow("event", "info", "callKey", DebugOnly("callValue"))

	if record.level != kratoslog.LevelInfo {
		t.Fatalf("level = %v, want info", record.level)
	}
	if calls != 1 {
		t.Fatalf("valuer calls = %d, want 1", calls)
	}
	for key, want := range map[string]any{
		"globalKey":  "globalValue",
		"boundKey":   "boundValue",
		"contextKey": "contextValue",
		"callKey":    "callValue",
	} {
		got, ok := fieldValue(record.keyvals, key)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("request debug field %q = %#v, %v, want %#v", key, got, ok, want)
		}
	}
}

func TestAtLevelKeepsExactLevelDuringRequestDebug(t *testing.T) {
	var record capturedLogRecord
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	logger := (&logger{shared: shared, config: &configState{
		level:       kratoslog.LevelInfo,
		filterEmpty: true,
		msgKey:      defaultMsgKey,
		output: loggerFunc(func(level kratoslog.Level, keyvals ...any) error {
			record = capturedLogRecord{level: level, keyvals: append([]any(nil), keyvals...)}
			return nil
		}),
	}}).WithContext(request.WithDebug(context.Background()))

	logger.Infow("debug", AtLevel(kratoslog.LevelDebug, "detail"))

	if _, ok := fieldValue(record.keyvals, "debug"); ok {
		t.Fatalf("AtLevel debug field leaked into request-debug info event: %#v", record.keyvals)
	}
}

func TestAtLevelPreservesNilWhenEmptyFilteringIsDisabled(t *testing.T) {
	var got []any
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	logger := &logger{shared: shared, config: &configState{
		level:       kratoslog.LevelDebug,
		filterEmpty: false,
		msgKey:      defaultMsgKey,
		output: loggerFunc(func(_ kratoslog.Level, keyvals ...any) error {
			got = append([]any(nil), keyvals...)
			return nil
		}),
	}}

	logger.Infow("debug.detail", AtLevel(kratoslog.LevelDebug, "detail"))

	value, ok := fieldValue(got, "debug.detail")
	if !ok || value != nil {
		t.Fatalf("debug.detail = %#v, %v, want present nil", value, ok)
	}
}

func fieldValue(keyvals []any, key string) (any, bool) {
	for i := 0; i+1 < len(keyvals); i += 2 {
		if keyvals[i] == key {
			return keyvals[i+1], true
		}
	}
	return nil, false
}
