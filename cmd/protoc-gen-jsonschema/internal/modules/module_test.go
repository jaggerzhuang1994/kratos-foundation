package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	schemaopts "github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/pluginpb"
	"sigs.k8s.io/yaml"
)

func TestModuleGeneratesJSONAndYAMLFromCodeGeneratorRequest(t *testing.T) {
	tests := []struct {
		name              string
		parameter         string
		wantFile          string
		wantSchemaVersion string
		definitionsKey    string
		refPrefix         string
		decode            func([]byte) ([]byte, error)
	}{
		{
			name:              "draft 07 JSON",
			parameter:         "draft=Draft07,output_file_suffix=.schema.json,preserve_proto_field_names=true,respect_protojson_int64=true",
			wantFile:          "contract.schema.json",
			wantSchemaVersion: draft07Version,
			definitionsKey:    "definitions",
			refPrefix:         "#/definitions/",
			decode: func(data []byte) ([]byte, error) {
				return data, nil
			},
		},
		{
			name:              "draft 2020-12 YAML",
			parameter:         "draft=Draft202012,output_file_suffix=.schema.yaml,preserve_proto_field_names=true,respect_protojson_int64=true",
			wantFile:          "contract.schema.yaml",
			wantSchemaVersion: draft202012Version,
			definitionsKey:    "$defs",
			refPrefix:         "#/$defs/",
			decode:            yaml.YAMLToJSON,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := runGenerator(t, contractRequest(t, test.parameter))
			if response.GetError() != "" {
				t.Fatalf("generator response error: %s", response.GetError())
			}
			if len(response.File) != 1 {
				t.Fatalf("generated files = %d, want 1", len(response.File))
			}

			generated := response.File[0]
			if generated.GetName() != test.wantFile {
				t.Fatalf("generated file name = %q, want %q", generated.GetName(), test.wantFile)
			}
			jsonData, err := test.decode([]byte(generated.GetContent()))
			if err != nil {
				t.Fatalf("decode generated artifact: %v\n%s", err, generated.GetContent())
			}

			var document map[string]any
			if err := json.Unmarshal(jsonData, &document); err != nil {
				t.Fatalf("unmarshal generated schema: %v\n%s", err, jsonData)
			}
			assertGeneratedContract(t, document, test.wantSchemaVersion, test.definitionsKey, test.refPrefix)
		})
	}
}

func contractRequest(t *testing.T, parameter string) *pluginpb.CodeGeneratorRequest {
	t.Helper()

	fileOptions := &descriptorpb.FileOptions{}
	proto.SetExtension(fileOptions, schemaopts.E_File, &schemaopts.FileOptions{
		EntrypointMessage: "Config",
		Title:             "Contract title",
		Description:       "Contract description",
	})

	messageOptions := &descriptorpb.MessageOptions{}
	proto.SetExtension(messageOptions, schemaopts.E_Message, &schemaopts.MessageOptions{
		Title: "Config object",
		Object: &schemaopts.ObjectKeywords{
			MinProperties: proto.Uint32(2),
		},
	})
	stringFieldOptions := &descriptorpb.FieldOptions{}
	proto.SetExtension(stringFieldOptions, schemaopts.E_Field, &schemaopts.FieldOptions{
		Title:       "Display name",
		Description: "Name shown to users",
		String_: &schemaopts.StringKeywords{
			MinLength: proto.Uint32(2),
			Pattern:   "^[a-z]+$",
		},
	})
	arrayFieldOptions := &descriptorpb.FieldOptions{}
	proto.SetExtension(arrayFieldOptions, schemaopts.E_Field, &schemaopts.FieldOptions{
		Array: &schemaopts.ArrayKeywords{MinItems: proto.Uint32(1)},
	})
	mapNumberOptions := &descriptorpb.FieldOptions{}
	proto.SetExtension(mapNumberOptions, schemaopts.E_Field, &schemaopts.FieldOptions{
		Numeric: &schemaopts.NumericKeywords{Min: &schemaopts.NumericKeywords_InclusiveMinimum{InclusiveMinimum: 0}},
	})

	enumOptions := &descriptorpb.EnumOptions{}
	proto.SetExtension(enumOptions, schemaopts.E_Enum, &schemaopts.EnumOptions{
		MappingType: schemaopts.EnumOptions_MapToCustom,
		Title:       "Lifecycle state",
	})
	readyOptions := &descriptorpb.EnumValueOptions{}
	proto.SetExtension(readyOptions, schemaopts.E_EnumValue, &schemaopts.EnumValueOptions{
		CustomValue: &anypb.Any{Value: []byte(`"ready"`)},
	})

	contract := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("contract.proto"),
		Package:    proto.String("contract.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/any.proto", "google/protobuf/duration.proto", "google/protobuf/struct.proto", "google/protobuf/timestamp.proto", "intstr.proto"},
		Options:    fileOptions,
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name:    proto.String("State"),
				Options: enumOptions,
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("STATE_UNSPECIFIED"), Number: proto.Int32(0)},
					{Name: proto.String("READY"), Number: proto.Int32(1), Options: readyOptions},
				},
			},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Child"),
				Field: []*descriptorpb.FieldDescriptorProto{
					scalarField("value", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				},
			},
			{
				Name:    proto.String("Config"),
				Options: messageOptions,
				NestedType: []*descriptorpb.DescriptorProto{
					mapEntry("LabelsEntry", descriptorpb.FieldDescriptorProto_TYPE_STRING),
					mapEntry("ScoresEntry", descriptorpb.FieldDescriptorProto_TYPE_INT64),
					mapEntry("LimitsEntry", descriptorpb.FieldDescriptorProto_TYPE_INT32),
					mapEntry("RatiosEntry", descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
					mapEntry("FlagsEntry", descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					mapEntry("BlobsEntry", descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					typedMapEntry("StatesByNameEntry", descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".contract.v1.State"),
					typedMapEntry("ChildrenByNameEntry", descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".contract.v1.Child"),
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					withFieldOptions(scalarField("display_name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING), stringFieldOptions),
					messageField("child", 2, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".contract.v1.Child"),
					withFieldOptions(messageField("children", 3, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Child"), arrayFieldOptions),
					messageField("labels", 4, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.LabelsEntry"),
					withFieldOptions(messageField("scores", 5, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.ScoresEntry"), mapNumberOptions),
					enumField("state", 6, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".contract.v1.State"),
					enumField("states", 7, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.State"),
					messageField("created_at", 8, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".google.protobuf.Timestamp"),
					messageField("delays", 9, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".google.protobuf.Duration"),
					messageField("metadata", 10, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".google.protobuf.Any"),
					enumField("deleted", 11, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".google.protobuf.NullValue"),
					messageField("selector", 12, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".k8s.io.apimachinery.pkg.util.intstr.IntOrString"),
					scalarField("count", 13, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					withFieldOptions(messageField("limits", 14, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.LimitsEntry"), mapNumberOptions),
					scalarField("enabled", 15, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					scalarField("ratio", 16, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
					scalarField("payload", 17, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					messageField("ratios", 18, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.RatiosEntry"),
					messageField("flags", 19, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.FlagsEntry"),
					messageField("blobs", 20, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.BlobsEntry"),
					messageField("states_by_name", 21, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.StatesByNameEntry"),
					messageField("children_by_name", 22, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".contract.v1.Config.ChildrenByNameEntry"),
				},
			},
			{
				Name: proto.String("Unused"),
				Field: []*descriptorpb.FieldDescriptorProto{
					scalarField("ignored", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				},
			},
		},
	}

	intstr := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("intstr.proto"),
		Package: proto.String("k8s.io.apimachinery.pkg.util.intstr"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("IntOrString"),
				Field: []*descriptorpb.FieldDescriptorProto{
					scalarField("str_val", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					scalarField("int_val", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				},
			},
		},
	}

	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"contract.proto"},
		Parameter:      proto.String(parameter),
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
			protodesc.ToFileDescriptorProto(durationpb.File_google_protobuf_duration_proto),
			protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto),
			protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto),
			intstr,
			contract,
		},
	}
}

func scalarField(name string, number int32, fieldType descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   fieldType.Enum(),
	}
}

func messageField(name string, number int32, label descriptorpb.FieldDescriptorProto_Label, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    label.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
	}
}

func enumField(name string, number int32, label descriptorpb.FieldDescriptorProto_Label, typeName string) *descriptorpb.FieldDescriptorProto {
	field := messageField(name, number, label, typeName)
	field.Type = descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum()
	return field
}

func withFieldOptions(field *descriptorpb.FieldDescriptorProto, options *descriptorpb.FieldOptions) *descriptorpb.FieldDescriptorProto {
	field.Options = options
	return field
}

func mapEntry(name string, valueType descriptorpb.FieldDescriptorProto_Type) *descriptorpb.DescriptorProto {
	return typedMapEntry(name, valueType, "")
}

func typedMapEntry(name string, valueType descriptorpb.FieldDescriptorProto_Type, valueTypeName string) *descriptorpb.DescriptorProto {
	value := scalarField("value", 2, valueType)
	if valueTypeName != "" {
		value.TypeName = proto.String(valueTypeName)
	}
	return &descriptorpb.DescriptorProto{
		Name:    proto.String(name),
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			scalarField("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			value,
		},
	}
}

func runGenerator(t *testing.T, request *pluginpb.CodeGeneratorRequest) *pluginpb.CodeGeneratorResponse {
	t.Helper()

	input, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal code generator request: %v", err)
	}
	var output bytes.Buffer
	pgs.Init(pgs.ProtocInput(bytes.NewReader(input)), pgs.ProtocOutput(&output)).
		RegisterModule(NewModule()).
		Render()

	response := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(output.Bytes(), response); err != nil {
		t.Fatalf("unmarshal code generator response: %v", err)
	}
	return response
}

func assertGeneratedContract(
	t *testing.T,
	document map[string]any,
	wantSchemaVersion string,
	definitionsKey string,
	refPrefix string,
) {
	t.Helper()

	if document["$schema"] != wantSchemaVersion {
		t.Errorf("$schema = %#v, want %q", document["$schema"], wantSchemaVersion)
	}
	if document["$ref"] != refPrefix+".contract.v1.Config" {
		t.Errorf("$ref = %#v", document["$ref"])
	}
	if document["title"] != "Contract title" || document["description"] != "Contract description" {
		t.Errorf("root metadata = title %#v, description %#v", document["title"], document["description"])
	}

	definitions, ok := document[definitionsKey].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", definitionsKey, document[definitionsKey])
	}
	if _, ok := definitions[".contract.v1.Unused"]; ok {
		t.Error("optimizer retained unlinked message .contract.v1.Unused")
	}
	config, ok := definitions[".contract.v1.Config"].(map[string]any)
	if !ok {
		t.Fatalf("entrypoint definition = %#v, want object", definitions[".contract.v1.Config"])
	}
	properties, ok := config["properties"].(map[string]any)
	if !ok {
		t.Fatalf("entrypoint properties = %#v, want object", config["properties"])
	}
	displayName, ok := properties["display_name"].(map[string]any)
	if !ok {
		t.Fatalf("display_name property = %#v, want object", properties["display_name"])
	}
	if displayName["$ref"] != refPrefix+".contract.v1.Config.display_name" {
		t.Errorf("display_name $ref = %#v", displayName["$ref"])
	}
	field, ok := definitions[".contract.v1.Config.display_name"].(map[string]any)
	if !ok || field["type"] != "string" || field["title"] != "Display name" || field["description"] != "Name shown to users" || field["minLength"] != float64(2) || field["pattern"] != "^[a-z]+$" {
		t.Errorf("display_name definition = %#v, want string schema", definitions[".contract.v1.Config.display_name"])
	}
	if config["title"] != "Config object" || config["minProperties"] != float64(2) {
		t.Errorf("Config options = %#v", config)
	}

	assertDefinition(t, definitions, ".contract.v1.Config.child", map[string]any{"$ref": refPrefix + ".contract.v1.Child"})
	childrenWant := map[string]any{
		"type":     "array",
		"minItems": float64(1),
		"items":    map[string]any{"$ref": refPrefix + ".contract.v1.Child"},
	}
	statesWant := map[string]any{
		"type":  "array",
		"items": map[string]any{"$ref": refPrefix + ".contract.v1.State"},
	}
	delaysWant := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string", "pattern": `^-?[0-9]+(\.[0-9]{1,9})?s$`},
	}
	delays := definitions[".contract.v1.Config.delays"].(map[string]any)
	items := delays["items"].(map[string]any)
	pattern, err := regexp.Compile(items["pattern"].(string))
	if err != nil {
		t.Fatalf("compile duration pattern: %v", err)
	}
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"0s", true}, {"1s", true}, {"-1s", true},
		{"1.5s", true}, {"-0.5s", true},
		{"0.000000001s", true}, {"-1.123456789s", true},
		{"1.1234567890s", false}, {"-1.1234567890s", false},
		{"1x5s", false}, {"1.s", false}, {".5s", false},
		{"1ms", false}, {"1m", false}, {"PT1S", false},
		{"1", false}, {"+1s", false}, {"--1s", false},
		{" 1s", false}, {"1s ", false}, {"1s\n", false}, {"", false},
	} {
		t.Run("duration/"+test.value, func(t *testing.T) {
			if got := pattern.MatchString(test.value); got != test.valid {
				t.Errorf("duration pattern accepts %q = %v, want %v", test.value, got, test.valid)
			}
		})
	}
	assertDefinition(t, definitions, ".contract.v1.Config.children", childrenWant)
	assertDefinition(t, definitions, ".contract.v1.Config.labels", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "string"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.scores", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "string", "format": "int64"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.limits", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "integer", "minimum": float64(0)},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.ratios", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "number"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.flags", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "boolean"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.blobs", map[string]any{
		"type": "object",
		"additionalProperties": map[string]any{
			"type":    "string",
			"pattern": "^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$",
		},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.states_by_name", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"$ref": refPrefix + ".contract.v1.State"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.children_by_name", map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"$ref": refPrefix + ".contract.v1.Child"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.state", map[string]any{"$ref": refPrefix + ".contract.v1.State"})
	assertDefinition(t, definitions, ".contract.v1.Config.states", statesWant)
	assertDefinition(t, definitions, ".contract.v1.State", map[string]any{
		"type":  "string",
		"title": "Lifecycle state",
		"enum":  []any{"STATE_UNSPECIFIED", "ready"},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.created_at", map[string]any{"type": "string", "format": "date-time"})
	assertDefinition(t, definitions, ".contract.v1.Config.delays", delaysWant)
	assertDefinition(t, definitions, ".contract.v1.Config.metadata", map[string]any{"type": "object"})
	assertDefinition(t, definitions, ".contract.v1.Config.deleted", map[string]any{"type": "null"})
	assertDefinition(t, definitions, ".contract.v1.Config.selector", map[string]any{"$ref": refPrefix + ".k8s.io.apimachinery.pkg.util.intstr.IntOrString"})
	assertDefinition(t, definitions, ".k8s.io.apimachinery.pkg.util.intstr.IntOrString", map[string]any{
		"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}},
	})
	assertDefinition(t, definitions, ".contract.v1.Config.count", map[string]any{"type": "string", "format": "int64"})
	assertDefinition(t, definitions, ".contract.v1.Config.enabled", map[string]any{"type": "boolean"})
	assertDefinition(t, definitions, ".contract.v1.Config.ratio", map[string]any{"type": "number"})
	assertDefinition(t, definitions, ".contract.v1.Config.payload", map[string]any{
		"type":    "string",
		"pattern": "^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$",
	})
}

func assertDefinition(t *testing.T, definitions map[string]any, name string, wantSubset map[string]any) {
	t.Helper()

	definition, ok := definitions[name].(map[string]any)
	if !ok {
		t.Fatalf("definition %s = %#v, want object", name, definitions[name])
	}
	for key, want := range wantSubset {
		if got := definition[key]; !equalJSONValue(got, want) {
			t.Errorf("definition %s[%q] = %#v, want %#v", name, key, got, want)
		}
	}
}

func equalJSONValue(got, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && bytes.Equal(gotJSON, wantJSON)
}

func TestModuleMergesJSONAndYAMLInputIntoGeneratedSchema(t *testing.T) {
	tests := []struct {
		name    string
		ext     string
		content string
	}{
		{
			name: "JSON",
			ext:  ".json",
			content: `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/External",
  "$defs": {"External": {"type": "object"}}
}`,
		},
		{
			name: "YAML",
			ext:  ".yaml",
			content: `$schema: https://json-schema.org/draft/2020-12/schema
$ref: '#/$defs/External'
$defs:
  External:
    type: object
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mergePath := filepath.Join(t.TempDir(), "merge"+test.ext)
			if err := os.WriteFile(mergePath, []byte(test.content), 0o600); err != nil {
				t.Fatalf("write merge schema: %v", err)
			}

			parameter := "draft=Draft202012,output_file_suffix=.schema.json,preserve_proto_field_names=true,respect_protojson_int64=true,merge=" + mergePath
			response := runGenerator(t, contractRequest(t, parameter))
			if response.GetError() != "" {
				t.Fatalf("generator response error: %s", response.GetError())
			}
			if len(response.File) != 1 {
				t.Fatalf("generated files = %d, want 1", len(response.File))
			}

			var document map[string]any
			if err := json.Unmarshal([]byte(response.File[0].GetContent()), &document); err != nil {
				t.Fatalf("decode merged output: %v", err)
			}
			if document["$ref"] != "#/$defs/.Merge_0" {
				t.Errorf("merged root $ref = %#v", document["$ref"])
			}
			definitions, ok := document["$defs"].(map[string]any)
			if !ok {
				t.Fatalf("$defs = %#v", document["$defs"])
			}
			assertDefinition(t, definitions, "External", map[string]any{"type": "object"})
			assertDefinition(t, definitions, ".Merge_0", map[string]any{
				"allOf": []any{
					map[string]any{"$ref": "#/$defs/.contract.v1.Config"},
					map[string]any{
						"$defs": map[string]any{"External": map[string]any{"type": "object"}},
						"allOf": []any{map[string]any{"$ref": "#/$defs/External"}},
					},
				},
			})
		})
	}
}

func TestModuleLoadsEverySupportedMergeDraft(t *testing.T) {
	versions := []string{
		draft04Version,
		draft06Version,
		draft07Version,
		draft201909Version,
		draft202012Version,
	}

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			mergePath := filepath.Join(t.TempDir(), "merge.json")
			content := fmt.Sprintf(`{"$schema":%q}`, version)
			if err := os.WriteFile(mergePath, []byte(content), 0o600); err != nil {
				t.Fatalf("write merge schema: %v", err)
			}

			module := NewModule()
			module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": mergePath}, "."))
			if module.mergeSchema == nil || module.mergeSchema.Schema() != version {
				t.Fatalf("loaded merge schema = %#v, want version %q", module.mergeSchema, version)
			}
		})
	}
}

func TestModuleRejectsUnreadableMalformedAndUnsupportedMergeSchemas(t *testing.T) {
	tempDir := t.TempDir()
	malformedPath := filepath.Join(tempDir, "malformed.json")
	if err := os.WriteFile(malformedPath, []byte(`{"$schema":`), 0o600); err != nil {
		t.Fatalf("write malformed schema: %v", err)
	}
	unsupportedPath := filepath.Join(tempDir, "unsupported.json")
	if err := os.WriteFile(unsupportedPath, []byte(`{"$schema":"https://example.com/unknown"}`), 0o600); err != nil {
		t.Fatalf("write unsupported schema: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		wantPrefix string
	}{
		{name: "unreadable", path: filepath.Join(tempDir, "missing.json"), wantPrefix: "failed read merge schema"},
		{name: "malformed", path: malformedPath, wantPrefix: "failed detect merge schema"},
		{name: "unsupported draft", path: unsupportedPath, wantPrefix: "unsupported merge schema https://example.com/unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := captureGeneratorFailure(t, func() {
				module := NewModule()
				module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": test.path}, "."))
			})
			if !strings.Contains(failure, test.wantPrefix) {
				t.Fatalf("failure = %q, want substring %q", failure, test.wantPrefix)
			}
		})
	}
}

type generatorFailure string

type panicDebugger struct{}

func (panicDebugger) Log(...any)               {}
func (panicDebugger) Logf(string, ...any)      {}
func (panicDebugger) Debug(...any)             {}
func (panicDebugger) Debugf(string, ...any)    {}
func (panicDebugger) Push(string) pgs.Debugger { return panicDebugger{} }
func (panicDebugger) Pop() pgs.Debugger        { return panicDebugger{} }
func (panicDebugger) Exit(code int)            { panic(generatorFailure(fmt.Sprintf("exit %d", code))) }
func (panicDebugger) Fail(values ...any)       { panic(generatorFailure(fmt.Sprint(values...))) }
func (panicDebugger) Failf(format string, v ...any) {
	panic(generatorFailure(fmt.Sprintf(format, v...)))
}
func (panicDebugger) CheckErr(err error, values ...any) {
	if err != nil {
		panic(generatorFailure(fmt.Sprintf("%s: %v", fmt.Sprint(values...), err)))
	}
}
func (debugger panicDebugger) Assert(ok bool, values ...any) {
	if !ok {
		debugger.Fail(values...)
	}
}

func captureGeneratorFailure(t *testing.T, run func()) (failure string) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("generator did not fail")
		}
		value, ok := recovered.(generatorFailure)
		if !ok {
			panic(recovered)
		}
		failure = string(value)
	}()
	run()
	return ""
}

var _ pgs.Debugger = panicDebugger{}

type mergeTransport func(*http.Request) (*http.Response, error)

func (f mergeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedMergeBody struct {
	io.Reader
	closed bool
}

func (b *trackedMergeBody) Close() error { b.closed = true; return nil }

type failedMergeReader struct{}

func (failedMergeReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRemoteMergeBoundaries(t *testing.T) {
	// 使用传输替身验证真实下载入口，避免依赖网络和真实等待。
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name         string
		status       int
		body         io.Reader
		transportErr error
		wantError    string
	}{
		{name: "success", status: 200, body: strings.NewReader(`{"$schema":"ok"}`)},
		{name: "HTTP failure", status: 503, body: strings.NewReader("unavailable"), wantError: "HTTP status 503"},
		{name: "over limit", status: 200, body: bytes.NewReader(bytes.Repeat([]byte{'x'}, (8<<20)+1)), wantError: "exceeds"},
		{name: "read failure", status: 200, body: failedMergeReader{}, wantError: "read merge schema"},
		{name: "transport failure", transportErr: errors.New("connection failed"), wantError: "download merge schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedMergeBody{Reader: tc.body}
			http.DefaultTransport = mergeTransport(func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("download request lacks bounded deadline")
				}
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
			})
			data, err := readRemoteMerge("https://example.test/merge.json")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || string(data) != `{"$schema":"ok"}` {
				t.Fatalf("data=%s error=%v", data, err)
			}
			if tc.transportErr == nil && !body.closed {
				t.Fatal("response body not closed")
			}
		})
	}
	// 覆盖模块选择远程输入的分支。
	http.DefaultTransport = mergeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"$schema":"https://json-schema.org/draft/2020-12/schema"}`)), Header: make(http.Header)}, nil
	})
	module := NewModule()
	module.InitContext(pgs.Context(panicDebugger{}, pgs.Parameters{"merge": "https://example.test/merge.json"}, "."))
	if module.mergeSchema == nil {
		t.Fatal("remote schema was not loaded")
	}
}

type mergeDebugRecorder struct {
	panicDebugger
	diagnostics string
}

func (d *mergeDebugRecorder) Debugf(format string, values ...any) {
	d.diagnostics += fmt.Sprintf(format, values...) + "\n"
}
func (d *mergeDebugRecorder) Push(string) pgs.Debugger { return d }
func (d *mergeDebugRecorder) Pop() pgs.Debugger        { return d }

func TestModuleMergeDiagnosticsHideSensitiveURLParts(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	// 保留原有按后缀选格式的行为；最后的 .json 使成功路径也经过完整 options 调试。
	location := "https://user-secret:password-secret@example.test/path-secret.json?signature=query-secret.json#fragment-secret.json"
	for _, tc := range []struct {
		name     string
		location string
		cause    error
		wantFail string
	}{
		{name: "debug summary", location: location},
		{name: "read failure", location: location, cause: errors.New("connection failed"), wantFail: "failed read merge schema"},
		{name: "format failure", location: strings.TrimSuffix(location, ".json"), wantFail: "unsupported output_file_suffix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = mergeTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/path-secret.json" || !strings.Contains(request.URL.RawQuery, "query-secret") {
					t.Fatal("diagnostic redaction changed the download URL")
				}
				if tc.cause != nil {
					return nil, tc.cause
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
					`{"$schema":"https://json-schema.org/draft/2020-12/schema"}`))}, nil
			})
			recorder := &mergeDebugRecorder{}
			initialize := func() {
				NewModule().InitContext(pgs.Context(recorder, pgs.Parameters{"merge": tc.location}, "."))
			}
			var diagnostic string
			if tc.wantFail != "" {
				diagnostic = captureGeneratorFailure(t, initialize)
				assertMergeSecretsAbsent(t, diagnostic)
				if !strings.Contains(diagnostic, tc.wantFail) || !strings.Contains(diagnostic, "https://example.test") {
					t.Fatalf("failure lost its operation or safe source: %s", diagnostic)
				}
			} else {
				initialize()
				diagnostic = recorder.diagnostics
				assertMergeSecretsAbsent(t, diagnostic)
				if !strings.Contains(diagnostic, "event=jsonschema.options") || !strings.Contains(diagnostic, "merge_enabled=true") {
					t.Fatalf("debug summary missing: %s", diagnostic)
				}
			}
		})
	}
}

func TestRemoteMergeDiagnosticPreservesNestedCauses(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	cause := errors.New("connection refused")
	redirectErr := &url.Error{
		Op:  "redirect",
		URL: "https://nested-user-secret:nested-password-secret@example.other/nested-path-secret?signature=nested-query-secret#nested-fragment-secret",
		Err: cause,
	}
	http.DefaultTransport = mergeTransport(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("follow redirect: %w", redirectErr)
	})
	_, err := readRemoteMerge("https://user-secret:password-secret@example.test/path-secret?signature=query-secret#fragment-secret")
	if !errors.Is(err, cause) || !errors.Is(err, redirectErr) {
		t.Fatalf("download error lost cause identity: %v", err)
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatal("download error lost url.Error type")
	}
	diagnostic := err.Error()
	assertMergeSecretsAbsent(t, diagnostic)
	for _, value := range []string{"download merge schema", "follow redirect", "connection refused", "https://example.test", "https://example.other"} {
		if !strings.Contains(diagnostic, value) {
			t.Fatalf("diagnostic lost %q: %s", value, diagnostic)
		}
	}
}

func TestMergeDiagnosticHandlesMalformedAndLocalSources(t *testing.T) {
	for _, tc := range []struct {
		location string
		want     string
	}{
		{location: "https://user-secret:%ZZ@example.test/path-secret?signature=query-secret", want: "remote URL"},
		{location: "/private/path-secret/merge.json", want: "local file"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			cause := fmt.Errorf("read %q failed", tc.location)
			err := redactMergeError(cause, tc.location)
			if !errors.Is(err, cause) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
			assertMergeSecretsAbsent(t, err.Error())
		})
	}
	if err := redactMergeError(nil, ""); err != nil {
		t.Fatalf("nil cause became %v", err)
	}
}

func assertMergeSecretsAbsent(t *testing.T, diagnostic string) {
	t.Helper()
	for _, secret := range []string{"user-secret", "password-secret", "path-secret", "query-secret", "fragment-secret"} {
		if strings.Contains(diagnostic, secret) {
			t.Fatalf("diagnostic exposes %q: %s", secret, diagnostic)
		}
	}
}
