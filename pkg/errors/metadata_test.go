package errors

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestErrorCopiesMetadataDataHeadersAndValidationDetails(t *testing.T) {
	original := New(422, "INVALID", "invalid").WithMetadata(map[string]string{
		"public":             "value",
		mdErrStackKey:        "private stack",
		mdReasonCodeKey:      "9001",
		mdHTTPCodeKey:        "599",
		mdHTTPDataKey:        "private data",
		mdHTTPHeadersKey:     "{}",
		mdValidationErrorKey: "[]",
	})
	public := original.PublicMetadata()
	if !reflect.DeepEqual(public, map[string]string{"public": "value"}) {
		t.Fatalf("PublicMetadata() = %#v", public)
	}
	public["public"] = "changed"
	if original.PublicMetadata()["public"] != "value" {
		t.Fatal("PublicMetadata returned aliased state")
	}

	input := map[string]any{"nested": map[string]any{"value": "before"}}
	withData := original.WithHTTPData(input)
	input["nested"].(map[string]any)["value"] = "after"
	gotData := withData.HTTPData().(map[string]any)
	if gotData["nested"].(map[string]any)["value"] != "before" {
		t.Fatalf("HTTPData snapshot = %#v", gotData)
	}
	gotData["nested"].(map[string]any)["value"] = "mutated"
	if again := withData.HTTPData().(map[string]any)["nested"].(map[string]any)["value"]; again != "before" {
		t.Fatalf("HTTPData getter returned aliased state: %#v", again)
	}

	withHeaders := original.
		WithHTTPHeaders(http.Header{"X-Trace": {"one", "one"}}).
		WithHTTPHeaders(http.Header{"X-Trace": {"one", "two"}})
	if got := withHeaders.HTTPHeaders().Values("X-Trace"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("HTTPHeaders() = %#v", got)
	}
	headers := withHeaders.HTTPHeaders()
	headers.Set("X-Trace", "changed")
	if got := withHeaders.HTTPHeaders().Values("X-Trace"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatal("HTTPHeaders returned aliased state")
	}
	corrupt := withHeaders.WithMetadata(map[string]string{mdHTTPHeadersKey: "{"})
	if got := corrupt.HTTPHeaders(); len(got) != 0 {
		t.Fatalf("corrupt headers = %#v", got)
	}

	details := []*ValidationError{{Field: "name", Reason: "required", ErrorName: "User"}}
	withValidation := original.WithValidationError(details)
	details[0].Field = "changed"
	if got := withValidation.ValidationError(); len(got) != 1 || got[0].Field != "name" {
		t.Fatalf("ValidationError() = %#v", got)
	}
}

func TestHTTPDataPreservesJSONNumbers(t *testing.T) {
	input := map[string]any{
		"id":     int64(9007199254740993),
		"values": []any{int64(-9223372036854775808), uint64(18446744073709551615), json.Number("1.234567890123456789")},
	}
	err := New(400, "INVALID", "invalid").WithHTTPData(input)
	for range 2 {
		data := err.HTTPData()
		encoded, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		const want = `{"id":9007199254740993,"values":[-9223372036854775808,18446744073709551615,1.234567890123456789]}`
		if string(encoded) != want {
			t.Fatalf("HTTP data = %s, want %s", encoded, want)
		}
		data.(map[string]any)["values"].([]any)[0] = 0
	}
}
