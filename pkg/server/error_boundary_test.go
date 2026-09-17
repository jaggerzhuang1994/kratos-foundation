package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
)

type boundaryLogger struct {
	log.Logger
	entries []string
	fields  []any
	context context.Context
}

type diagnosticBoundaryError struct {
	verboseFormats *int
}

func (e *diagnosticBoundaryError) Error() string { return "database unavailable" }

func (e *diagnosticBoundaryError) Format(state fmt.State, verb rune) {
	if verb == 'v' && state.Flag('+') {
		*e.verboseFormats++
		_, _ = fmt.Fprint(state, "database unavailable detail")
		return
	}
	_, _ = fmt.Fprint(state, e.Error())
}

func (l *boundaryLogger) WithContext(ctx context.Context) log.Logger {
	l.context = ctx
	return l
}
func (l *boundaryLogger) With(fields ...any) log.Logger {
	l.fields = append([]any(nil), fields...)
	return l
}
func (l *boundaryLogger) Error(message ...any) {
	l.entries = append(l.entries, fmt.Sprint(message...)+fmt.Sprint(l.fields...))
	l.fields = nil
}

func TestNormalizeErrorsRecordsOnlyServerFailure(t *testing.T) {
	cause := errors.New("permission store unavailable")
	for _, tc := range []struct {
		name       string
		err        error
		code, logs int
	}{
		{"success", nil, 200, 0},
		{"rejected", foundationerrors.New(403, "FORBIDDEN", "denied"), 403, 0},
		{"cancelled", context.Canceled, 499, 0},
		{"storage", cause, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := &boundaryLogger{}
			ctx := context.Background()
			result, err := normalizeErrors(logger)(func(context.Context, any) (any, error) { return "reply", tc.err })(ctx, "secret-request")
			if result != "reply" || foundationerrors.Code(err) != tc.code || len(logger.entries) != tc.logs {
				t.Fatalf("reply=%v err=%v logs=%v", result, err, logger.entries)
			}
			if tc.logs > 0 {
				if logger.context != ctx || !errors.Is(err, cause) || !strings.Contains(logger.entries[0], cause.Error()) {
					t.Fatal("missing context or cause")
				}
				if strings.Contains(logger.entries[0], "secret-request") {
					t.Fatal("request leaked")
				}
			}
		})
	}
}

func TestRecoveryDoesNotExposeRequestOrPanicPayload(t *testing.T) {
	logger := &boundaryLogger{}
	handler := recoverRequests(logger)(normalizeErrors(logger)(func(context.Context, any) (any, error) { panic("secret-panic") }))
	reply, err := handler(context.Background(), "secret-request")
	if reply != nil || foundationerrors.Code(err) != 500 || len(logger.entries) != 1 {
		t.Fatalf("reply=%v err=%v logs=%v", reply, err, logger.entries)
	}
	if strings.Contains(logger.entries[0], "secret-panic") || strings.Contains(logger.entries[0], "secret-request") || strings.Contains(err.Error(), "secret-") {
		t.Fatal("sensitive panic information leaked")
	}
	if foundationerrors.FromError(foundationerrors.FromError(err).GRPCStatus().Err()).ErrStack() == "" {
		t.Fatal("panic stack lost in gRPC response")
	}
	if !strings.Contains(logger.entries[0], "panic_type") {
		t.Fatal("panic type missing")
	}
}

func TestRequestFailureDiagnosticsFollowRequestDebug(t *testing.T) {
	logger, path := newBoundaryTestLogger(t)
	for index, debug := range []bool{false, true} {
		ctx := context.Background()
		if debug {
			ctx = request.WithDebug(ctx)
		}
		before := logSize(t, path)
		verboseFormats := 0
		cause := &diagnosticBoundaryError{verboseFormats: &verboseFormats}
		_, err := normalizeErrors(logger)(func(context.Context, any) (any, error) {
			return nil, fmt.Errorf("load account: %w", cause)
		})(ctx, nil)
		if !errors.Is(err, cause) {
			t.Fatalf("case %d error = %v", index, err)
		}
		line := logDelta(t, path, before)
		if !strings.Contains(line, "error=load account: database unavailable") {
			t.Fatalf("case %d error summary missing: %s", index, line)
		}
		if got := strings.Contains(line, "error.detail="); got != debug {
			t.Fatalf("case %d debug=%v error.detail present=%v: %s", index, debug, got, line)
		}
		if got := verboseFormats > 0; got != debug {
			t.Fatalf("case %d debug=%v verbose error formatted=%v", index, debug, got)
		}
	}
}

func TestRequestPanicStackFollowsRequestDebug(t *testing.T) {
	logger, path := newBoundaryTestLogger(t)
	for index, debug := range []bool{false, true} {
		ctx := context.Background()
		if debug {
			ctx = request.WithDebug(ctx)
		}
		before := logSize(t, path)
		_, _ = recoverRequests(logger)(func(context.Context, any) (any, error) {
			panic("private panic payload")
		})(ctx, nil)
		line := logDelta(t, path, before)
		if !strings.Contains(line, "panic_type=string") {
			t.Fatalf("case %d panic type missing: %s", index, line)
		}
		if got := strings.Contains(line, "stack="); got != debug {
			t.Fatalf("case %d debug=%v stack present=%v: %s", index, debug, got, line)
		}
		if strings.Contains(line, "private panic payload") {
			t.Fatalf("case %d panic payload leaked: %s", index, line)
		}
	}
}

func newBoundaryTestLogger(t *testing.T) (log.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.log")
	logger, cleanup, err := testlog.New(testlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: true,
		TimeFormat:  time.RFC3339,
		Std:         testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return logger, path
}

func logSize(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(data)
}

func logDelta(t *testing.T, path string, offset int) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset > len(data) {
		t.Fatalf("log offset %d exceeds size %d", offset, len(data))
	}
	return string(data[offset:])
}
