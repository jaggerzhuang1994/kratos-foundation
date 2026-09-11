package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
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

func TestSnapshotPreservesJSONNumbers(t *testing.T) {
	current, err := New([]*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(`{"limits":{"signed":9223372036854775807,"unsigned":18446744073709551615,"nested":[-9223372036854775808,9007199254740993]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"limits.signed":   json.Number("9223372036854775807"),
		"limits.unsigned": json.Number("18446744073709551615"),
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

func TestParserExpandsEnvironmentBeforeDecoding(t *testing.T) {
	t.Setenv("CONFIG_ENV_KEY", "limits")
	t.Setenv("CONFIG_ENV_NUMBER", "9223372036854775807")
	t.Setenv("CONFIG_ENV_BOOL", "true")
	t.Setenv("CONFIG_ENV_DEFAULT", "")
	for _, tt := range []struct{ format, content string }{
		{"json", `{"${CONFIG_ENV_KEY}":{"number":${CONFIG_ENV_NUMBER},"enabled":$CONFIG_ENV_BOOL,"port":${CONFIG_ENV_DEFAULT:-8080}}}`},
		{"yaml", "${CONFIG_ENV_KEY}:\n  number: ${CONFIG_ENV_NUMBER}\n  enabled: $CONFIG_ENV_BOOL\n  port: ${CONFIG_ENV_DEFAULT:-8080}\n"},
	} {
		t.Run(tt.format, func(t *testing.T) {
			source := &kratosconfig.KeyValue{Key: "${CONFIG_ENV_KEY}", Format: tt.format, Value: []byte(tt.content)}
			current, err := New([]*kratosconfig.KeyValue{source})
			if err != nil {
				t.Fatal(err)
			}
			if got, found := current.Lookup("limits.number"); !found || fmt.Sprint(got) != "9223372036854775807" {
				t.Fatalf("number = %v, %t", got, found)
			}
			if got, _ := current.Lookup("limits.port"); fmt.Sprint(got) != "8080" {
				t.Fatalf("default port = %v", got)
			}
			if got, _ := current.Lookup("limits.enabled"); got != true {
				t.Fatalf("enabled = %v", got)
			}
			if source.Key != "${CONFIG_ENV_KEY}" || string(source.Value) != tt.content {
				t.Fatal("source was mutated")
			}
		})
	}
	source := &kratosconfig.KeyValue{Key: "${CONFIG_ENV_KEY}.raw", Value: []byte("${CONFIG_ENV_NUMBER}")}
	first, err := New([]*kratosconfig.KeyValue{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_ENV_NUMBER", "42")
	second, err := New([]*kratosconfig.KeyValue{source})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := first.Lookup("limits.raw"); got != "9223372036854775807" {
		t.Fatalf("old snapshot = %v", got)
	}
	if got, _ := second.Lookup("limits.raw"); got != "42" {
		t.Fatalf("new snapshot = %v", got)
	}
}

func TestParserRejectsUnresolvedNumericEnvironment(t *testing.T) {
	t.Setenv("CONFIG_ENV_MISSING", "")
	if err := os.Unsetenv("CONFIG_ENV_MISSING"); err != nil {
		t.Fatal(err)
	}
	if _, err := New([]*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(`{"value":${CONFIG_ENV_MISSING}}`)}}); err == nil {
		t.Fatal("invalid expanded JSON accepted")
	}
}

func TestParserDoesNotResolveConfigReferences(t *testing.T) {
	t.Setenv("CONFIG_ENV_LITERAL", "${CONFIG_ENV_MISSING}")
	t.Setenv("cycle", "")
	if err := os.Unsetenv("cycle"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_ENV_MISSING", "")
	if err := os.Unsetenv("CONFIG_ENV_MISSING"); err != nil {
		t.Fatal(err)
	}
	current, err := New([]*kratosconfig.KeyValue{
		{Key: "config.json", Format: "json", Value: []byte(`{
   "CONFIG_ENV_MISSING":"config-value",
   "value":"${CONFIG_ENV_MISSING}",
   "injected":"${CONFIG_ENV_LITERAL}",
   "cycle":"${cycle}"
  }`)},
		{Key: "override.json", Format: "json", Value: []byte(`{"CONFIG_ENV_MISSING":"overridden"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"value":    "",
		"injected": "${CONFIG_ENV_MISSING}", "cycle": "",
	} {
		if got, found := current.Lookup(key); !found || got != want {
			t.Fatalf("%s = %v, %t; want %s", key, got, found, want)
		}
	}
}
