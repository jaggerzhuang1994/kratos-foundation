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
