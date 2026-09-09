package main

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
	"testing"
)

func TestProtocVersionFormatsSuffixAndMissingVersion(t *testing.T) {
	if got := protocVersion(&protogen.Plugin{Request: new(pluginpb.CodeGeneratorRequest)}); got != "(unknown)" {
		t.Fatalf("missing protoc version = %q", got)
	}
	plugin := &protogen.Plugin{Request: &pluginpb.CodeGeneratorRequest{
		CompilerVersion: &pluginpb.Version{
			Major:  proto.Int32(27),
			Minor:  proto.Int32(3),
			Patch:  proto.Int32(1),
			Suffix: proto.String("rc1"),
		},
	}}
	if got := protocVersion(plugin); got != "v27.3.1-rc1" {
		t.Fatalf("protoc version = %q", got)
	}
}
