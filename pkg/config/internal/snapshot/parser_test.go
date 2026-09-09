package snapshot

import (
	"encoding/json"
	"strings"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestParserDecodesRawJSONAndYAMLValues(t *testing.T) {
	t.Parallel()

	current, err := New([]*kratosconfig.KeyValue{
		{Key: "service.host", Value: []byte("localhost")},
		{Key: "config.json", Format: "json", Value: []byte(`{"service":{"port":8080}}`)},
		{Key: "config.yaml", Format: "yaml", Value: []byte("feature:\n  enabled: true\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]any{
		"service.host":    "localhost",
		"service.port":    json.Number("8080"),
		"feature.enabled": true,
	}
	for path, want := range checks {
		got, found := current.Lookup(path)
		if !found || got != want {
			t.Fatalf("Lookup(%q) = (%#v, %t), want %#v", path, got, found, want)
		}
	}
}

func TestParserRejectsUnsupportedAndMalformedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  *kratosconfig.KeyValue
		needle string
	}{
		{
			name:   "unsupported format",
			value:  &kratosconfig.KeyValue{Key: "config.toml", Format: "toml", Value: []byte("value=1")},
			needle: "unsupported",
		},
		{
			name:   "malformed json",
			value:  &kratosconfig.KeyValue{Key: "config.json", Format: "json", Value: []byte(`{"value":`)},
			needle: "decode config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New([]*kratosconfig.KeyValue{tt.value})
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("New error = %v, want substring %q", err, tt.needle)
			}
		})
	}
}

func TestSnapshotPreservesJSONNumbersAndReferences(t *testing.T) {
	current, err := New([]*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(`{"limits":{"signed":9223372036854775807,"unsigned":18446744073709551615,"nested":[-9223372036854775808,9007199254740993]},"reference":"${limits.unsigned}"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"limits.signed":   json.Number("9223372036854775807"),
		"limits.unsigned": json.Number("18446744073709551615"),
		"reference":       "18446744073709551615",
	} {
		if got, found := current.Lookup(path); !found || got != want {
			t.Fatalf("Lookup(%q) = %v, want %v", path, got, want)
		}
	}
	nested, _ := current.Lookup("limits.nested")
	got := nested.([]any)
	if got[0] != json.Number("-9223372036854775808") || got[1] != json.Number("9007199254740993") {
		t.Fatalf("nested = %v", got)
	}
}

func TestSnapshotRejectsInvalidOrTrailingJSON(t *testing.T) {
	for _, content := range []string{`{"value":`, `{"value":01}`, `{"value":1} {"value":2}`, `{"value":1} true`, `{"value":1} trailing`} {
		t.Run(content, func(t *testing.T) {
			_, err := New([]*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(content)}})
			if err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
}
