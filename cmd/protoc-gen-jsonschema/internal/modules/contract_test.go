package modules

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"

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
