package snapshot

import (
	"strings"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

func TestResolverExpandsReferencesFallbacksAndScalarChains(t *testing.T) {
	t.Parallel()

	current, err := New([]*kratosconfig.KeyValue{{
		Key:    "config.json",
		Format: "json",
		Value: []byte(`{
			"host": "localhost",
			"port": 8080,
			"address": "${host}:${port}",
			"endpoint": "http://${address}",
			"enabled": true,
			"enabled_text": "${enabled}",
			"fallback": "${missing:ready}",
			"empty_fallback": "${missing:}",
			"colon_fallback": "${missing:http://localhost:8080}",
			"items": ["${endpoint}", {"value": "${fallback}"}]
		}`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	wants := map[string]string{
		"address":        "localhost:8080",
		"endpoint":       "http://localhost:8080",
		"enabled_text":   "true",
		"fallback":       "ready",
		"empty_fallback": "",
		"colon_fallback": "http://localhost:8080",
	}
	for path, want := range wants {
		value, found := current.Lookup(path)
		if !found || value != want {
			t.Fatalf("%s = (%#v, %t), want %q", path, value, found, want)
		}
	}
	items, found := current.Lookup("items")
	if !found {
		t.Fatal("items are missing")
	}
	list := items.([]any)
	if list[0] != "http://localhost:8080" || list[1].(map[string]any)["value"] != "ready" {
		t.Fatalf("items = %#v", list)
	}
}

func TestResolverRejectsMissingNonScalarAndCyclicReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		needle string
	}{
		{name: "missing", input: `{"value":"${missing}"}`, needle: `reference "missing" is missing`},
		{name: "non scalar", input: `{"object":{"x":1},"value":"${object}"}`, needle: "non-scalar"},
		{name: "cycle", input: `{"first":"${second}","second":"${third}","third":"${first}"}`, needle: "second -> third -> first -> second"},
		{name: "empty reference", input: `{"value":"${ :fallback}"}`, needle: "reference path is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New([]*kratosconfig.KeyValue{{Key: "config.json", Format: "json", Value: []byte(tt.input)}})
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("New error = %v, want substring %q", err, tt.needle)
			}
		})
	}
}
