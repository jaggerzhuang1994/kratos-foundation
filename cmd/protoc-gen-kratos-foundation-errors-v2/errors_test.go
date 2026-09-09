package main

import (
	"flag"
	"fmt"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
	"strings"
	"testing"
)

func TestGeneratedIsHelperMatchesStableIdentity(t *testing.T) {
	wrapper := &errorWrapper{
		Errors: []*errorInfo{{
			Value:          "VALIDATOR",
			HTTPCode:       422,
			CamelValue:     "Validator",
			CommentLiteral: `"validation failed"`,
			NumberValue:    422,
		}},
		Symbols: errorGoSymbols{
			ErrorType: "errors.Error",
			FromError: "errors.FromError",
			New:       "errors.New",
			Sprintf:   "fmt.Sprintf",
			Sprint:    "fmt.Sprint",
		},
		ErrorStackSkip: defaultErrorStackSkip,
	}

	content, err := wrapper.execute()
	if err != nil {
		t.Fatalf("render errors template: %v", err)
	}
	want := `return e.Reason == "VALIDATOR" && e.Metadata != nil && e.Metadata["reason_code"] == "422"`
	if !strings.Contains(content, want) {
		t.Fatalf("generated Is helper does not match stable identity %q:\n%s", want, content)
	}
	if strings.Contains(content, "e.Code ==") {
		t.Fatalf("generated Is helper still compares transport HTTP code:\n%s", content)
	}
}

// TestStackSkipPluginOption 验证默认值和自定义值都直接写入产物，不引入跨文件包级声明。
func TestStackSkipPluginOption(t *testing.T) {
	tests := []struct {
		name      string
		parameter string
		wantSkip  int
	}{
		{name: "default", wantSkip: defaultErrorStackSkip},
		{name: "custom", parameter: "stack_skip=7", wantSkip: 7},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := generateStackSkipFixture(t, test.parameter)
			if response.GetError() != "" {
				t.Fatalf("generator response error: %s", response.GetError())
			}
			if len(response.File) != 2 {
				t.Fatalf("generated file count = %d, want 2", len(response.File))
			}

			wantCall := fmt.Sprintf(".WithErrStack(%d)", test.wantSkip)
			for _, file := range response.File {
				content := file.GetContent()
				if strings.Contains(content, "generatedErrorStackSkip") {
					t.Fatalf("%s contains a package-level stack-skip declaration", file.GetName())
				}
				if !strings.Contains(content, wantCall) {
					t.Fatalf("%s does not contain %q:\n%s", file.GetName(), wantCall, content)
				}
			}
		})
	}
}

func generateStackSkipFixture(t *testing.T, parameter string) *pluginpb.CodeGeneratorResponse {
	t.Helper()

	flags := flag.NewFlagSet("stack-skip-test", flag.ContinueOnError)
	errorStackSkip := registerGeneratorFlags(flags)
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"first.proto", "second.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			stackSkipProtoFixture("first.proto", "FirstFailure", "FIRST_FAILURE", 500),
			stackSkipProtoFixture("second.proto", "SecondFailure", "SECOND_FAILURE", 503),
		},
	}
	if parameter != "" {
		request.Parameter = proto.String(parameter)
	}

	plugin, err := (protogen.Options{ParamFunc: flags.Set}).New(request)
	if err != nil {
		t.Fatalf("create protogen plugin: %v", err)
	}
	if err := run(plugin, *errorStackSkip); err != nil {
		t.Fatalf("run generator: %v", err)
	}
	return plugin.Response()
}

func stackSkipProtoFixture(
	filename string,
	enumName string,
	errorName string,
	httpCode int32,
) *descriptorpb.FileDescriptorProto {
	errorOptions := new(descriptorpb.EnumValueOptions)
	proto.SetExtension(errorOptions, kratoserrors.E_Code, httpCode)
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String(filename),
		Package: proto.String("stackskip.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/stackskip/v1;stackskipv1"),
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String(enumName),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String(enumName + "_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String(errorName), Number: proto.Int32(1), Options: errorOptions},
			},
		}},
	}
}
