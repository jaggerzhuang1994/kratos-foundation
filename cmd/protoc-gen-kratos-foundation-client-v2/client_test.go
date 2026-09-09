package main

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/pluginpb"
	"strings"
	"testing"
)

func TestRunGeneratesUnaryClientContract(t *testing.T) {
	plugin := newClientGeneratorFixture(t, false)
	if err := run(plugin); err != nil {
		t.Fatal(err)
	}
	response := plugin.Response()
	if response.GetSupportedFeatures() != uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL) {
		t.Fatalf("supported features = %d", response.GetSupportedFeatures())
	}
	if len(response.File) != 1 {
		t.Fatalf("generated files = %d, want 1", len(response.File))
	}
	file := response.File[0]
	if file.GetName() != "example.com/orders/v1/order_client.pb.go" {
		t.Fatalf("generated filename = %q", file.GetName())
	}
	content := file.GetContent()
	wants := []string{
		"type OrderService interface",
		"func NewOrderService(",
		"NewOrderServiceWithConnName(factory, \"v1\")",
		"c.factory.AcquireClient(ctx, c.connName)",
		"defer release()",
		"google.api.http annotation is required",
	}
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("generated client does not contain %q:\n%s", want, content)
		}
	}
	for _, removed := range []string{"WithDefaultConnName", "ConnNameFromContext"} {
		if strings.Contains(content, removed) {
			t.Fatalf("generated client still uses removed context helper %q", removed)
		}
	}
}

func TestRunRejectsStreamingRPC(t *testing.T) {
	plugin := newClientGeneratorFixture(t, true)
	err := run(plugin)
	if err == nil || !strings.Contains(err.Error(), "streaming RPC is not supported") {
		t.Fatalf("run streaming fixture error = %v", err)
	}
}

func TestRunUsesExplicitServiceConnectionName(t *testing.T) {
	optionFile, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("client_options.proto"),
		Package:    proto.String("kratos_foundation_client"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Extension: []*descriptorpb.FieldDescriptorProto{{
			Name:     proto.String("client_name"),
			Number:   proto.Int32(51000),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			Extendee: proto.String(".google.protobuf.ServiceOptions"),
		}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	option := dynamicpb.NewExtensionType(optionFile.Extensions().Get(0))
	for _, name := range []string{"orders-service", "", " orders-service"} {
		t.Run(name, func(t *testing.T) {
			fixture := newClientGeneratorFixture(t, false)
			service := fixture.Request.ProtoFile[0].Service[0]
			service.Options = new(descriptorpb.ServiceOptions)
			proto.SetExtension(service.Options, option, name)
			fixture, err := (protogen.Options{}).New(fixture.Request)
			if err != nil {
				t.Fatal(err)
			}
			err = run(fixture)
			if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
				if err == nil {
					t.Fatalf("invalid client_name %q accepted", name)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if content := fixture.Response().File[0].GetContent(); !strings.Contains(content, `NewOrderServiceWithConnName(factory, "orders-service")`) {
				t.Fatalf("service connection name not bound in generated constructor:\n%s", content)
			}
		})
	}
}

func TestDefaultServiceNameUsesStablePathFallbacks(t *testing.T) {
	tests := []struct {
		path string
		pkg  protogen.GoPackageName
		want string
	}{
		{path: "orders/v1/order.proto", pkg: "ordersv1", want: "v1"},
		{path: "order.proto", pkg: "ordersv1", want: "order"},
	}
	for _, test := range tests {
		if got := defaultServiceName(test.path, test.pkg); got != test.want {
			t.Fatalf("defaultServiceName(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func newClientGeneratorFixture(t *testing.T, streaming bool) *protogen.Plugin {
	t.Helper()
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"orders/v1/order.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("orders/v1/order.proto"),
			Package: proto.String("orders.v1"),
			Syntax:  proto.String("proto3"),
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("example.com/orders/v1;ordersv1"),
			},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("GetRequest")},
				{Name: proto.String("GetReply")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("OrderService"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:            proto.String("Get"),
					InputType:       proto.String(".orders.v1.GetRequest"),
					OutputType:      proto.String(".orders.v1.GetReply"),
					ClientStreaming: proto.Bool(streaming),
				}},
			}},
		}},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatal(err)
	}
	return plugin
}
