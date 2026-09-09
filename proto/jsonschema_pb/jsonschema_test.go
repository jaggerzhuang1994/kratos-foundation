package jsonschema_pb

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestFileOptionExtensionRoundTrips(t *testing.T) {
	options := new(descriptorpb.FileOptions)
	want := &FileOptions{EntrypointMessage: "Config", Title: "Foundation"}
	proto.SetExtension(options, E_File, want)

	got, ok := proto.GetExtension(options, E_File).(*FileOptions)
	if !ok || !proto.Equal(got, want) {
		t.Fatalf("file extension = %#v, want %#v", got, want)
	}
}
