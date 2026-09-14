package errors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type legacyError struct {
	kratoserrors.Status
	cause error
}

func (e *legacyError) Error() string { return e.Message }

func (e *legacyError) Unwrap() error { return e.cause }

func (e *legacyError) GRPCStatus() *status.Status {
	s, _ := status.New(codes.Unknown, e.Message).WithDetails(&errdetails.ErrorInfo{Reason: e.Reason, Metadata: e.Metadata})
	return s
}

func TestLegacyErrorPreservesStatusAndPrivateState(t *testing.T) {
	cause := stderrors.New("database unavailable")
	metadata := map[string]string{
		"reason_code": "42201", "err_stack": "private-stack", "http_header": `{"Retry-After":"3"}`,
		"http_data": `{"id":9007199254740993}`, "field": "email",
	}
	original := &legacyError{Status: kratoserrors.Status{Code: 422, Reason: "VALIDATOR", Message: "invalid request", Metadata: metadata}, cause: cause}
	snapshot := make(map[string]string, len(metadata))
	for k, v := range metadata {
		snapshot[k] = v
	}
	converted := FromError(fmt.Errorf("validate request: %w", original))
	if converted.Code != 422 || converted.ReasonCode() != 42201 || converted.HTTPHeaders().Get("Retry-After") != "3" {
		t.Fatalf("converted=%+v", converted)
	}
	if !stderrors.Is(converted, cause) || !strings.Contains(converted.ErrStack(), "private-stack") {
		t.Fatal("lost local diagnostics")
	}
	if got := converted.HTTPData().(map[string]any)["id"]; got != json.Number("9007199254740993") {
		t.Fatalf("precision lost: %v", got)
	}
	if got := converted.PublicMetadata(); !reflect.DeepEqual(got, map[string]string{"field": "email"}) {
		t.Fatalf("public metadata=%v", got)
	}
	restored := FromError(status.Convert(Normalize(original)).Err())
	if restored.Code != 422 || restored.ReasonCode() != 42201 || !reflect.DeepEqual(restored.HTTPData(), converted.HTTPData()) {
		t.Fatalf("round trip=%v", restored)
	}
	if !strings.Contains(restored.ErrStack(), "private-stack") || len(restored.HTTPHeaders()) != 0 {
		t.Fatal("gRPC lost diagnostics or forwarded response headers")
	}
	if !reflect.DeepEqual(metadata, snapshot) {
		t.Fatal("original metadata mutated")
	}
}

func TestOuterStructuredErrorWinsOverCause(t *testing.T) {
	inner := New(404, "INNER", "inner")
	outer := &legacyError{Status: kratoserrors.Status{Code: 422, Reason: "OUTER", Message: "outer"}, cause: inner}
	if got := FromError(fmt.Errorf("wrap: %w", outer)); got.Code != 422 || got.Reason != "OUTER" {
		t.Fatalf("outer lost: %v", got)
	}
	joined := stderrors.Join(stderrors.New("plain"), outer)
	if got := FromError(joined); got.Reason != "OUTER" {
		t.Fatalf("joined error lost: %v", got)
	}
}

func TestNormalizeUnknownAndContextErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   int
		reason string
	}{
		{"unknown", stderrors.New("sql password=private"), 500, "UNKNOWN"},
		{"grpc internal", status.Error(codes.Internal, "sql password=private"), 500, "UNKNOWN"},
		{"grpc deadline", status.Error(codes.DeadlineExceeded, "private"), 504, "DEADLINE_EXCEEDED"},
		{"grpc cancel", status.Error(codes.Canceled, "private"), 499, "CLIENT_CANCELED"},
		{"cancel", fmt.Errorf("query: %w", context.Canceled), 499, "CLIENT_CANCELED"},
		{"deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), 504, "DEADLINE_EXCEEDED"},
		{"explicit server", &legacyError{Status: kratoserrors.Status{Code: 503, Reason: "UNAVAILABLE", Message: "try later"}}, 503, "UNAVAILABLE"},
		{"invalid status", New(200, "BROKEN", "private"), 500, "UNKNOWN"},
	}
	if Normalize(nil) != nil {
		t.Fatal("nil changed")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromError(Normalize(tt.err))
			if int(got.Code) != tt.code || got.Reason != tt.reason || strings.Contains(got.Message, "private") {
				t.Fatalf("normalized=%v", got)
			}
			if !stderrors.Is(got, tt.err) {
				t.Fatal("cause lost")
			}
		})
	}
}

func TestIncomingLegacyStackRemainsPrivateToHTTP(t *testing.T) {
	s, err := status.New(codes.Unknown, "invalid").WithDetails(&errdetails.ErrorInfo{Reason: "VALIDATOR", Metadata: map[string]string{"err_stack": "private-stack", "http_header": `{"X-Internal":"private"}`, "http_headers": `{"X-Internal":["private"]}`, "reason_code": "422"}})
	if err != nil {
		t.Fatal(err)
	}
	restored := FromError(s.Err())
	if restored.Code != 500 {
		t.Fatal("must not infer HTTP status from business code")
	}
	if !strings.Contains(restored.ErrStack(), "private-stack") || len(restored.HTTPHeaders()) != 0 || strings.Contains(fmt.Sprint(restored.PublicMetadata()), "private") {
		t.Fatal("remote private metadata escaped")
	}
}

func TestNormalizePreservesExplicitRemoteTimeout(t *testing.T) {
	original := New(504, "UPSTREAM_TIMEOUT", "please retry").WithReasonCode(50042)
	normalized := FromError(Normalize(original.GRPCStatus().Err()))
	if normalized.Code != 504 || normalized.Reason != "UPSTREAM_TIMEOUT" || normalized.ReasonCode() != 50042 {
		t.Fatalf("remote semantic changed: %v", normalized)
	}
}

func TestNormalizeInfrastructureCancellationKeepsCancellation(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  int
	}{
		{context.Canceled, 499}, {context.DeadlineExceeded, 504},
	} {
		input := New(500, "INTERNAL_SERVER", "Internal Server Error").WithCause(tc.cause)
		output := Normalize(input)
		if Code(output) != tc.code || !stderrors.Is(output, tc.cause) {
			t.Fatalf("output=%v", output)
		}
	}
	// 明确的客户端业务拒绝仍由外层状态决定。
	denied := New(403, "FORBIDDEN", "denied").WithCause(context.Canceled)
	if Code(Normalize(denied)) != 403 {
		t.Fatal("explicit business rejection overwritten")
	}
}
