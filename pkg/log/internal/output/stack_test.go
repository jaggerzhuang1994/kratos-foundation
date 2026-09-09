package output

import (
	"errors"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"os"
	"testing"
)

func TestNewStackAttemptsEveryLoggerAndJoinsErrors(t *testing.T) {
	t.Parallel()

	firstErr := errors.New("first failed")
	thirdErr := errors.New("third failed")
	first := &recordingLogger{err: firstErr}
	second := new(recordingLogger)
	third := &recordingLogger{err: thirdErr}

	err := NewStack(first, second, third).Log(kratoslog.LevelError, "message", "failed")
	if !errors.Is(err, firstErr) || !errors.Is(err, thirdErr) {
		t.Fatalf("Log() error = %v, want both sink errors", err)
	}
	for index, recorder := range []*recordingLogger{first, second, third} {
		if len(recorder.calls) != 1 {
			t.Fatalf("sink %d call count = %d, want 1", index, len(recorder.calls))
		}
	}
}

func TestNewStackStopsAfterClosedLogger(t *testing.T) {
	t.Parallel()

	priorErr := errors.New("prior failed")
	first := &recordingLogger{err: priorErr}
	closed := &recordingLogger{err: os.ErrClosed}
	later := new(recordingLogger)

	err := NewStack(first, closed, later).Log(kratoslog.LevelError, "message", "failed")
	if !errors.Is(err, priorErr) || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Log() error = %v, want prior error and os.ErrClosed", err)
	}
	for index, recorder := range []*recordingLogger{first, closed} {
		if len(recorder.calls) != 1 {
			t.Fatalf("sink %d call count = %d, want 1", index, len(recorder.calls))
		}
	}
	if len(later.calls) != 0 {
		t.Fatalf("later sink call count = %d, want 0 after os.ErrClosed", len(later.calls))
	}
}
