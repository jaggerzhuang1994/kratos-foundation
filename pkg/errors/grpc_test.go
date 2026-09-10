package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGRPCStatusRoundTripPreservesHTTPCode(t *testing.T) {
	for _, code := range []int{400, 422, 502, 505, 599} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			original := New(code, "FAILURE", "failed").
				WithReasonCode(9001).
				WithMetadata(map[string]string{
					mdHTTPCodeKey: "404",
					"public":      "value",
				})

			grpcStatus := original.GRPCStatus()
			detail := grpcErrorInfoDetail(t, grpcStatus)
			if got, want := detail.Metadata[mdHTTPCodeKey], strconv.Itoa(code); got != want {
				t.Fatalf("gRPC HTTP code metadata = %q, want %q", got, want)
			}
			if _, exposed := original.PublicMetadata()[mdHTTPCodeKey]; exposed {
				t.Fatal("HTTP code metadata is public")
			}

			restored := FromError(roundTripGRPCStatus(t, grpcStatus).Err())
			if got := int(restored.Code); got != code {
				t.Fatalf("restored HTTP code = %d, want %d", got, code)
			}
			if got := restored.Reason; got != "FAILURE" {
				t.Fatalf("restored reason = %q, want FAILURE", got)
			}
			if got := restored.ReasonCode(); got != 9001 {
				t.Fatalf("restored reason code = %d, want 9001", got)
			}
			if got := restored.PublicMetadata()["public"]; got != "value" {
				t.Fatalf("restored public metadata = %q, want value", got)
			}
			if _, exposed := restored.PublicMetadata()[mdHTTPCodeKey]; exposed {
				t.Fatal("restored HTTP code metadata is public")
			}
		})
	}
}

func TestFromErrorRestoresOnlyConsistentUnknownHTTPCode(t *testing.T) {
	tests := []struct {
		name     string
		grpcCode codes.Code
		metadata map[string]string
		wantCode int
	}{
		{name: "unknown", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "422"}, wantCode: 422},
		{name: "missing", grpcCode: codes.Unknown, wantCode: 500},
		{name: "invalid", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "invalid"}, wantCode: 500},
		{name: "overflow", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "9223372036854775808"}, wantCode: 500},
		{name: "below error range", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "399"}, wantCode: 500},
		{name: "above status range", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "600"}, wantCode: 500},
		{name: "inconsistent outer status", grpcCode: codes.Unknown, metadata: map[string]string{mdHTTPCodeKey: "404"}, wantCode: 500},
		{name: "known status ignores metadata", grpcCode: codes.InvalidArgument, metadata: map[string]string{mdHTTPCodeKey: "422"}, wantCode: 400},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grpcStatus, err := status.New(test.grpcCode, "failed").WithDetails(&errdetails.ErrorInfo{
				Reason:   "FAILURE",
				Metadata: test.metadata,
			})
			if err != nil {
				t.Fatalf("attach gRPC error details: %v", err)
			}

			if got := int(FromError(grpcStatus.Err()).Code); got != test.wantCode {
				t.Fatalf("restored HTTP code = %d, want %d", got, test.wantCode)
			}
		})
	}
}

func roundTripGRPCStatus(t *testing.T, grpcStatus *status.Status) *status.Status {
	t.Helper()
	encoded, err := proto.Marshal(grpcStatus.Proto())
	if err != nil {
		t.Fatalf("marshal gRPC status: %v", err)
	}
	decoded := new(statuspb.Status)
	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal gRPC status: %v", err)
	}
	return status.FromProto(decoded)
}

func grpcErrorInfoDetail(t *testing.T, grpcStatus *status.Status) *errdetails.ErrorInfo {
	t.Helper()
	for _, detail := range grpcStatus.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatal("gRPC status has no ErrorInfo detail")
	return nil
}

func TestFromErrorHandlesPlainAndGRPCStatuses(t *testing.T) {
	plain := stderrors.New("plain failure")
	converted := FromError(plain)
	if converted.Code != kratoserrors.UnknownCode || converted.Reason != kratoserrors.UnknownReason || converted.Message != plain.Error() {
		t.Fatalf("plain error conversion = %+v", converted)
	}
	if FromError(nil) != nil {
		t.Fatal("FromError(nil) != nil")
	}
	original := New(409, "CONFLICT", "conflict")
	if FromError(fmt.Errorf("wrapped: %w", original)) != original {
		t.Fatal("FromError did not preserve wrapped package error")
	}

	grpcStatus := status.New(codes.PermissionDenied, "denied")
	withoutDetails := FromError(grpcStatus.Err())
	if withoutDetails.Code != 403 || withoutDetails.Reason != kratoserrors.UnknownReason {
		t.Fatalf("gRPC status conversion = %+v", withoutDetails)
	}
	withDetails, detailErr := grpcStatus.WithDetails(&errdetails.ErrorInfo{
		Reason:   "FORBIDDEN",
		Metadata: map[string]string{"tenant": "acme"},
	})
	if detailErr != nil {
		t.Fatal(detailErr)
	}
	restored := FromError(withDetails.Err())
	if restored.Code != 403 || restored.Reason != "FORBIDDEN" || restored.PublicMetadata()["tenant"] != "acme" {
		t.Fatalf("gRPC details conversion = %+v", restored)
	}
}

func TestGRPCStatusRoundTripPreservesHTTPData(t *testing.T) {
	want := map[string]any{"rpId": "example.com", "credentialId": "-_8AAQ", "uid": json.Number("9007199254740993")}
	original := New(400, "CREDENTIAL_REVOKED", "revoked").WithReasonCode(40123).WithHTTPData(want).WithErrStack()
	restored := FromError(roundTripGRPCStatus(t, original.GRPCStatus()).Err())
	if got := restored.HTTPData(); !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP data = %#v, want %#v", got, want)
	}
	if restored.Code != 400 || restored.ReasonCode() != 40123 {
		t.Fatalf("error identity changed: %v", restored)
	}
	if _, ok := restored.PublicMetadata()[mdHTTPDataKey]; ok {
		t.Fatal("HTTP data leaked into public metadata")
	}
	if restored.ErrStack() != "" {
		t.Fatal("error stack crossed gRPC")
	}
	restored.HTTPData().(map[string]any)["rpId"] = "changed"
	if got := restored.HTTPData().(map[string]any)["rpId"]; got != "example.com" {
		t.Fatalf("HTTP data snapshot mutated: %v", got)
	}
	// 再次转发仍保留 data，覆盖多级网关场景。
	forwarded := FromError(roundTripGRPCStatus(t, restored.GRPCStatus()).Err())
	if !reflect.DeepEqual(forwarded.HTTPData(), want) {
		t.Fatalf("forwarded data = %#v", forwarded.HTTPData())
	}
}

func TestFromErrorIgnoresMalformedHTTPData(t *testing.T) {
	for _, raw := range []string{"", "{", "{} trailing"} {
		t.Run(raw, func(t *testing.T) {
			wireStatus, err := status.New(codes.InvalidArgument, "invalid").WithDetails(&errdetails.ErrorInfo{Reason: "INVALID", Metadata: map[string]string{mdHTTPDataKey: raw, mdReasonCodeKey: "40123"}})
			if err != nil {
				t.Fatal(err)
			}
			restored := FromError(roundTripGRPCStatus(t, wireStatus).Err())
			if restored.HTTPData() != nil || restored.ReasonCode() != 40123 {
				t.Fatalf("malformed data changed error: %v", restored)
			}
		})
	}
	invalid := New(400, "INVALID", "invalid").WithHTTPData(make(chan int))
	if got := FromError(invalid.GRPCStatus().Err()); got.HTTPData() != nil || got.Reason != "INVALID" {
		t.Fatalf("unencodable data changed error: %v", got)
	}
}
