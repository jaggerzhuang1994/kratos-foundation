package log

import (
	"context"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"testing"
)

func TestDerivedPublicSettingsAreLocal(t *testing.T) {
	old := GetLogger()
	t.Cleanup(func() { SetLogger(old) })
	state := &sharedState{}
	state.custom.Store(&customState{})
	var written []any
	base := &logger{shared: state, config: &configState{level: kratoslog.LevelInfo, msgKey: "msg", output: loggerFunc(func(_ kratoslog.Level, kv ...any) error { written = kv; return nil })}}
	SetLogger(base)
	l := WithLevel(kratoslog.LevelDebug).WithFilterKeys("secret").With("secret", "hidden")
	l.Debug("visible")
	if !containsLogValue(written, "visible") || containsLogValue(written, "hidden") {
		t.Fatalf("derived settings=%v", written)
	}
	written = nil
	base.Debug("hidden")
	if written != nil {
		t.Fatal("derived level changed parent")
	}
	for _, view := range []Logger{WithFilterKeys("token"), With("key", "value"), WithContext(request.WithDebug(context.Background()))} {
		view.Info("test")
	}
	if state.custom.Load().version != 0 {
		t.Fatal("package derivation changed shared state")
	}
	for _, invalid := range []func(){func() { WithLevel(99) }} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid programmatic setting did not panic")
				}
			}()
			invalid()
		}()
	}
}
