package modules

import (
	"encoding/json"
	schemaopts "github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"
	"reflect"
	"testing"
)

type fqdn string

func (f fqdn) FullyQualifiedName() string { return string(f) }

func TestGeneratorPureHelpersPreserveReferencesAndTypes(t *testing.T) {
	if got := toRefId(fqdn("package.Message")); got != "package.Message" {
		t.Fatalf("toRefId = %q", got)
	}
	if !isInt64(pgs.Int64T) || !isInt64(pgs.Fixed64T) || isInt64(pgs.Int32T) {
		t.Fatal("isInt64 classification is wrong")
	}
	remaining := deletePropertyInRequired([]string{"a", "b", "a"}, "a")
	if len(remaining) != 1 || remaining[0] != "b" {
		t.Fatalf("remaining properties = %#v", remaining)
	}
}

func TestVisibilityFiltersPropertiesAndRequiredTogether(t *testing.T) {
	messageOptions := &descriptorpb.MessageOptions{}
	proto.SetExtension(messageOptions, schemaopts.E_Message, &schemaopts.MessageOptions{VisibilityLevel: 1})
	fieldOptions := &descriptorpb.FieldOptions{}
	proto.SetExtension(fieldOptions, schemaopts.E_Field, &schemaopts.FieldOptions{VisibilityLevel: 1})
	visible := scalarField("visible", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)
	visible.Options = fieldOptions
	hidden := scalarField("hidden", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING)
	for _, test := range []struct {
		name      string
		parameter string
		want      []string
	}{
		{name: "default includes all fields", parameter: "entrypoint_message=Config", want: []string{"visible", "hidden"}},
		{name: "explicit zero includes all fields", parameter: "entrypoint_message=Config,visibility_level=0", want: []string{"visible", "hidden"}},
		{name: "positive threshold filters hidden fields", parameter: "entrypoint_message=Config,visibility_level=1", want: []string{"visible"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := runGenerator(t, &pluginpb.CodeGeneratorRequest{
				Parameter:      proto.String(test.parameter),
				FileToGenerate: []string{"visibility.proto"},
				ProtoFile: []*descriptorpb.FileDescriptorProto{{
					Name: proto.String("visibility.proto"), Package: proto.String("visibility"), Syntax: proto.String("proto3"),
					MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Config"), Options: messageOptions, Field: []*descriptorpb.FieldDescriptorProto{visible, hidden}}},
				}},
			})
			if response.GetError() != "" || len(response.File) != 1 {
				t.Fatalf("generation failed: %v", response)
			}
			var document struct {
				Definitions map[string]struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal([]byte(response.File[0].GetContent()), &document); err != nil {
				t.Fatal(err)
			}
			root := document.Definitions[".visibility.Config"]
			if len(root.Properties) != len(test.want) || !reflect.DeepEqual(root.Required, test.want) {
				t.Fatalf("properties = %v, required = %v, want %v", root.Properties, root.Required, test.want)
			}
			for _, name := range test.want {
				if _, exists := root.Properties[name]; !exists {
					t.Fatalf("missing property %s", name)
				}
			}
			if len(test.want) == 1 {
				if _, exists := document.Definitions[".visibility.Config.hidden"]; exists {
					t.Fatal("hidden field definition remains in artifact")
				}
			}
		})
	}
}

func TestParseScalarValueFromAny(t *testing.T) {
	tests := []struct {
		name    string
		value   *anypb.Any
		want    any
		wantErr bool
	}{
		{name: "nil Any"},
		{name: "empty Any", value: &anypb.Any{}},
		{name: "string", value: &anypb.Any{Value: []byte(`"text"`)}, want: "text"},
		{name: "number", value: &anypb.Any{Value: []byte(`42.5`)}, want: 42.5},
		{name: "boolean", value: &anypb.Any{Value: []byte(`true`)}, want: true},
		{name: "null", value: &anypb.Any{Value: []byte(`null`)}},
		{name: "array", value: &anypb.Any{Value: []byte(`[1,"two"]`)}, want: []any{float64(1), "two"}},
		{name: "object", value: &anypb.Any{Value: []byte(`{"key":"value"}`)}, want: map[string]any{"key": "value"}},
		{name: "malformed", value: &anypb.Any{Value: []byte(`{`)}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseScalaValueFromAny(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseScalaValueFromAny() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseScalaValueFromAny() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseScalaValueFromAny() = %#v, want %#v", got, test.want)
			}
		})
	}
}
