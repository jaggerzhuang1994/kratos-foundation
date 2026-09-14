package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
)

func TestEncoderEncodesFoundationErrorAndHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := foundationerrors.New(http.StatusTooManyRequests, "RATE_LIMITED", "slow down").WithReasonCode(42).WithMetadata(map[string]string{"request_id": "r1"}).WithHTTPData(map[string]string{"retry": "later"}).WithHTTPHeaders(http.Header{"Retry-After": {"5"}})
	Encoder()(recorder, httptest.NewRequest(http.MethodGet, "/", nil), err)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "5" {
		t.Fatalf("status=%d headers=%v", recorder.Code, recorder.Header())
	}
	if got := recorder.Body.String(); !strings.Contains(got, `"code":42`) || !strings.Contains(got, `"request_id":"r1"`) || !strings.Contains(got, `"retry":"later"`) {
		t.Fatalf("unexpected body: %s", got)
	}
}

func TestEncoderMapsUnknownErrorToInternalServerError(t *testing.T) {
	recorder := httptest.NewRecorder()
	Encoder()(recorder, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("sql: password=private"))
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != internalServerErrorBody {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "private") {
		t.Fatalf("internal error details leaked in response: %s", recorder.Body.String())
	}
}

func TestDecoderRoundTripsErrorAndClosesBody(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader(`{"code":17,"message":"bad","data":{"field":"name"},"reason":"INVALID","metadata":{"public":"yes"}}`)}
	err := Decoder()(context.Background(), &http.Response{StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Header: http.Header{"X-Trace": {"t"}}, Body: body})
	se := foundationerrors.FromError(err)
	if se == nil || se.Reason != "INVALID" || se.ReasonCode() != 17 || se.PublicMetadata()["public"] != "yes" || se.HTTPHeaders().Get("X-Trace") != "t" || !body.closed {
		t.Fatalf("decoded=%v closed=%t", se, body.closed)
	}
	if Decoder()(context.Background(), &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("ignored"))}) != nil {
		t.Fatal("successful response returned an error")
	}
}

func TestDecoderFallsBackForMalformedBody(t *testing.T) {
	err := Decoder()(context.Background(), &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(strings.NewReader("not-json"))})
	se := foundationerrors.FromError(err)
	if se == nil || se.Code != http.StatusBadGateway || se.Message != "502 Bad Gateway" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrorDataRoundTripPreservesLargeNumbers(t *testing.T) {
	const data = `{"id":9007199254740993,"values":[-9223372036854775808,18446744073709551615,1.234567890123456789]}`
	recorder := httptest.NewRecorder()
	Encoder()(recorder, httptest.NewRequest(http.MethodGet, "/", nil),
		foundationerrors.New(400, "INVALID", "invalid").WithHTTPData(json.RawMessage(data)))
	decoded := Decoder()(t.Context(), recorder.Result())
	encoded, err := json.Marshal(foundationerrors.HTTPData(decoded))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != data {
		t.Fatalf("round trip data = %s, want %s", encoded, data)
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error { r.closed = true; return nil }

func TestDecoderLimitsActualErrorBodyReadAndClosesBody(t *testing.T) {
	for _, length := range []int64{-1, 1, 16 << 20} {
		t.Run(strconv.FormatInt(length, 10), func(t *testing.T) {
			body := &countingErrorBody{remaining: 16 << 20}
			err := Decoder()(t.Context(), &http.Response{StatusCode: 502, Status: "502 Bad Gateway", ContentLength: length, Body: body})
			if body.read > (1<<20)+1 {
				t.Errorf("read %d bytes, want at most 1 MiB + 1", body.read)
			}
			if !body.closed {
				t.Error("oversized error body was not closed")
			}
			se := foundationerrors.FromError(err)
			if se == nil || se.Code != 502 || se.Message != "502 Bad Gateway" {
				t.Fatalf("unexpected status error: %v", err)
			}
			if se.Unwrap() == nil || !strings.Contains(se.Unwrap().Error(), "exceeds") {
				t.Fatalf("missing controlled size error: %v", err)
			}
		})
	}
}

func TestDecoderAcceptsErrorBodyAtLimit(t *testing.T) {
	const prefix = `{"code":17,"reason":"INVALID","message":"`
	const suffix = `"}`
	message := strings.Repeat("a", (1<<20)-len(prefix)-len(suffix))
	body := &trackingReadCloser{Reader: strings.NewReader(prefix + message + suffix)}
	err := Decoder()(t.Context(), &http.Response{StatusCode: 400, Body: body})
	se := foundationerrors.FromError(err)
	if se == nil {
		t.Fatalf("valid error body at limit returned no business error: %v", err)
	}
	if se.Reason != "INVALID" || se.Message != message || se.ReasonCode() != 17 || !body.closed {
		t.Fatalf("valid error body at limit failed: reason=%q, closed=%t", se.Reason, body.closed)
	}
}

type countingErrorBody struct {
	remaining, read int
	closed          bool
}

func (b *countingErrorBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *countingErrorBody) Close() error { b.closed = true; return nil }

func TestEncoderPreservesLegacyExplicitStatuses(t *testing.T) {
	for _, code := range []int{422, 503} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			// Kratos 状态访问器与旧 cyberkite 错误具有相同契约。
			legacy := kratoserrors.New(code, "LEGACY", "safe message").WithMetadata(map[string]string{"reason_code": "1234", "http_data": `{"id":9007199254740993}`, "http_header": `{"Retry-After":"7"}`, "err_stack": "private-stack"})
			recorder := httptest.NewRecorder()
			Encoder()(recorder, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("wrapped: %w", legacy))
			body := recorder.Body.String()
			if recorder.Code != code || recorder.Header().Get("Retry-After") != "7" || !strings.Contains(body, `"code":1234`) || !strings.Contains(body, `9007199254740993`) || !strings.Contains(body, `"reason":"LEGACY"`) || strings.Contains(body, "private") {
				t.Fatalf("code=%d header=%v body=%s", recorder.Code, recorder.Header(), body)
			}
		})
	}
}

func TestGatewayFiltersDiagnosticsReceivedOverGRPC(t *testing.T) {
	origin := foundationerrors.New(500, "INTERNAL", "internal error").WithMetadata(map[string]string{"err_stack": "private-origin-frame"}).WithCause(errors.New("private-database-cause"))
	remote := origin.GRPCStatus().Err()
	if !strings.Contains(foundationerrors.ErrStack(remote), "private-origin-frame") || !strings.Contains(foundationerrors.ErrStack(remote), "private-database-cause") {
		t.Fatal("gateway did not receive complete diagnostics")
	}
	recorder := httptest.NewRecorder()
	Encoder()(recorder, httptest.NewRequest(http.MethodGet, "/", nil), remote)
	if recorder.Code != 500 || strings.Contains(recorder.Body.String(), "private-") || strings.Contains(recorder.Body.String(), "err_stack") {
		t.Fatalf("gateway exposed diagnostics: %s", recorder.Body.String())
	}
}
