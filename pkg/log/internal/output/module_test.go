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
		{"missing", "", []any{"msg", "hello"}, []any{"msg", "hello", "module", "unknown"}},
		{"fixed", "orders", []any{"module", "other", "module", "last"}, []any{"module", "orders"}},
		{"last valid", "", []any{"module", "first", "module", "last", "module", 12}, []any{"module", "last"}},
		{"invalid", "", []any{"module", nil, "module", "", "module", " bad "}, []any{"module", "unknown"}},
		{"odd module", "", []any{"module"}, []any{"module", "unknown"}},
		{"odd field", "", []any{"field"}, []any{"field", "(MISSING)", "module", "unknown"}},
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
