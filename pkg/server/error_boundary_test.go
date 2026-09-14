package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type boundaryLogger struct {
	log.Logger
	entries []string
	fields  []any
	context context.Context
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
	if !strings.Contains(logger.entries[0], "stack") || !strings.Contains(logger.entries[0], "panic_type") {
		t.Fatal("panic diagnostic missing")
	}
}
