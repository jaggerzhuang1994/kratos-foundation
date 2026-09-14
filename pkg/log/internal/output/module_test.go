package output

import (
	"errors"
	"reflect"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestModuleNormalization(t *testing.T) {
	for _, test := range []struct {
		name, fixed string
		fields      []any
		want        []any
	}{
		{"missing", "", []any{"msg", "hello"}, []any{"module", "unknown", "msg", "hello"}},
		{"display order", "orders", []any{"a", 1, "caller", "app.go:10", "ts", "now", "b", 2, "msg", "hello"},
			[]any{"ts", "now", "module", "orders", "caller", "app.go:10", "a", 1, "b", 2, "msg", "hello"}},
		{"without timestamp", "", []any{"a", 1, "caller", "app.go:10"},
			[]any{"module", "unknown", "caller", "app.go:10", "a", 1}},
		{"duplicate header fields", "", []any{"ts", "first", "caller", "first", "a", 1, "ts", "last", "caller", "last"},
			[]any{"ts", "first", "ts", "last", "module", "unknown", "caller", "first", "caller", "last", "a", 1}},
		{"fixed", "orders", []any{"module", "other", "module", "last"}, []any{"module", "orders"}},
		{"last valid", "", []any{"module", "first", "module", "last", "module", 12}, []any{"module", "last"}},
		{"invalid", "", []any{"module", nil, "module", "", "module", " bad "}, []any{"module", "unknown"}},
		{"odd module", "", []any{"module"}, []any{"module", "unknown"}},
		{"odd field", "", []any{"field"}, []any{"module", "unknown", "field", "(MISSING)"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("write failed")
			recorder := &recordingLogger{err: failure}
			original := append([]any(nil), test.fields...)
			if err := NewModule(recorder, test.fixed).Log(kratoslog.LevelWarn, test.fields...); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.calls[0].keyvals, test.want) {
				t.Fatalf("got %v, want %v", recorder.calls[0].keyvals, test.want)
			}
			if !reflect.DeepEqual(test.fields, original) {
				t.Fatal("modified caller fields")
			}
		})
	}
}
