package output

import (
	"errors"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"reflect"
	"testing"
)

func TestNewDedupeKeepsLastStringValueAtFirstPosition(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("write failed")
	recorder := &recordingLogger{err: wantErr}
	logger := NewDedupe(recorder)
	err := logger.Log(
		kratoslog.LevelInfo,
		"request.id", "first",
		"status", 200,
		"request.id", "last",
		7, "non-string-first",
		7, "non-string-last",
		"odd",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Log() error = %v, want %v", err, wantErr)
	}
	want := []any{
		"request.id", "last",
		"status", 200,
		7, "non-string-first",
		7, "non-string-last",
		"odd",
	}
	if got := recorder.calls[0].keyvals; !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated keyvals = %#v, want %#v", got, want)
	}
}
