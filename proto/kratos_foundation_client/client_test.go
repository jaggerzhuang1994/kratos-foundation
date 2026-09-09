package kratos_foundation_client

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestClientNameExtensionRoundTrips(t *testing.T) {
	options := new(descriptorpb.ServiceOptions)
	proto.SetExtension(options, E_ClientName, "orders")

	got, ok := proto.GetExtension(options, E_ClientName).(string)
	if !ok || got != "orders" {
		t.Fatalf("client_name extension = %#v, want orders", got)
	}
}
