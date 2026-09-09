package output

import (
	"os"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestStdLoggerRoutesByLevel(t *testing.T) {
	t.Parallel()

	stdout := new(recordingLogger)
	stderr := new(recordingLogger)
	logger := &stdLogger{stdout: stdout, stderr: stderr}

	for _, level := range []kratoslog.Level{kratoslog.LevelDebug, kratoslog.LevelInfo, kratoslog.LevelWarn} {
		if err := logger.Log(level, "level", level); err != nil {
			t.Fatal(err)
		}
	}
	for _, level := range []kratoslog.Level{kratoslog.LevelError, kratoslog.LevelFatal} {
		if err := logger.Log(level, "level", level); err != nil {
			t.Fatal(err)
		}
	}
	if len(stdout.calls) != 3 || len(stderr.calls) != 2 {
		t.Fatalf("stdout/stderr calls = %d/%d, want 3/2", len(stdout.calls), len(stderr.calls))
	}
}

func TestNewStdRoutesToCapturedStandardStreams(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stdout.Close(); err != nil {
			t.Errorf("close stdout capture: %v", err)
		}
	})
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stderr.Close(); err != nil {
			t.Errorf("close stderr capture: %v", err)
		}
	})

	previousStdout, previousStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = previousStdout, previousStderr })
	logger := NewStd()
	os.Stdout, os.Stderr = previousStdout, previousStderr

	if err := logger.Log(kratoslog.LevelInfo, "message", "stdout-entry"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(kratoslog.LevelError, "message", "stderr-entry"); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Sync(); err != nil {
		t.Fatal(err)
	}

	stdoutData, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderrData, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stdoutData), "stdout-entry") || strings.Contains(string(stdoutData), "stderr-entry") {
		t.Fatalf("stdout = %q, want only info entry", stdoutData)
	}
	if !strings.Contains(string(stderrData), "stderr-entry") || strings.Contains(string(stderrData), "stdout-entry") {
		t.Fatalf("stderr = %q, want only error entry", stderrData)
	}
}
