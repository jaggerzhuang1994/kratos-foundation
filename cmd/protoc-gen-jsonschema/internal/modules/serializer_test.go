package modules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
)

func TestSerializerUnserializesJsonYamlAndRejectsUnknownSuffix(t *testing.T) {
	serializer := NewSerializerImpl(&proto.PluginOptions{OutputFileSuffix: ".json"}, true)
	var jsonValue map[string]any
	if err := serializer.Unserialize([]byte(`{"name":"json"}`), &jsonValue, ""); err != nil || jsonValue["name"] != "json" {
		t.Fatalf("JSON = %#v, %v", jsonValue, err)
	}
	var yamlValue map[string]any
	if err := serializer.Unserialize([]byte("name: yaml\n"), &yamlValue, ".yaml"); err != nil || yamlValue["name"] != "yaml" {
		t.Fatalf("YAML = %#v, %v", yamlValue, err)
	}
	if err := serializer.Unserialize([]byte("{}"), &jsonValue, ".toml"); err == nil {
		t.Fatal("unknown suffix was accepted")
	}
}

func TestPrettyJSONOption(t *testing.T) {
	for _, tc := range []struct {
		parameter string
		pretty    bool
	}{
		{"", true}, {"pretty_json_output=true", true}, {"pretty_json_output=false", false},
	} {
		t.Run(tc.parameter, func(t *testing.T) {
			response := runGenerator(t, contractRequest(t, tc.parameter))
			if response.GetError() != "" || len(response.File) != 1 {
				t.Fatalf("response: %v", response)
			}
			content := response.File[0].GetContent()
			if !json.Valid([]byte(content)) {
				t.Fatal("output is invalid JSON")
			}
			if strings.Contains(content, "\n") != tc.pretty {
				t.Fatalf("pretty output = %v, want %v", strings.Contains(content, "\n"), tc.pretty)
			}
		})
	}
	failure := captureGeneratorFailure(t, func() {
		NewModule().InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"pretty_json_output": "invalid"}, "."))
	})
	if !strings.Contains(failure, "invalid pretty_json_output") {
		t.Fatalf("failure = %s", failure)
	}
}
