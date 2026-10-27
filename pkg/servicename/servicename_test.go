package servicename

import (
	"strings"
	"testing"

	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestFromPathUsesDirectoryThenFileThenGoPackage(t *testing.T) {
	tests := []struct {
		path, goPackage, want string
	}{
		{path: "wallet_service/wallet_service.proto", goPackage: "wallet_service_pb", want: "wallet_service"},
		{path: "team_service/team_work_inner_service.proto", goPackage: "team_work_inner_service_pb", want: "team_service"},
		{path: "order.proto", goPackage: "orderspb", want: "order"},
		{path: "", goPackage: "orderspb", want: "orderspb"},
		// 只有完整的版本名才算版本，包含版本前缀的普通名字不受影响。
		{path: "v2ray/proxy.proto", goPackage: "v2raypb", want: "v2ray"},
		{path: "vault/secret.proto", goPackage: "vaultpb", want: "vault"},
	}
	for _, test := range tests {
		got, err := FromPath(test.path, test.goPackage)
		if err != nil || got != test.want {
			t.Fatalf("FromPath(%q, %q) = %q, %v, want %q", test.path, test.goPackage, got, err, test.want)
		}
	}
}

func TestFromPathRejectsVersionAndEmptyNames(t *testing.T) {
	tests := []struct {
		path, goPackage, wantErrorHas string
	}{
		{path: "orders/v1/order.proto", goPackage: "ordersv1", wantErrorHas: `"v1" is an API version`},
		{path: "orders/v2beta1/order.proto", goPackage: "ordersv2", wantErrorHas: `"v2beta1" is an API version`},
		{path: "orders/v1alpha/order.proto", goPackage: "ordersv1", wantErrorHas: "API version"},
		{path: "orders/v1p1beta1/order.proto", goPackage: "ordersv1", wantErrorHas: "API version"},
		{path: "v3.proto", goPackage: "pb", wantErrorHas: "API version"},
		{path: "", goPackage: "v1", wantErrorHas: "API version"},
		{path: "", goPackage: "", wantErrorHas: "cannot derive service name"},
	}
	for _, test := range tests {
		got, err := FromPath(test.path, test.goPackage)
		if err == nil || !strings.Contains(err.Error(), test.wantErrorHas) {
			t.Fatalf("FromPath(%q, %q) = %q, %v, want error containing %q", test.path, test.goPackage, got, err, test.wantErrorHas)
		}
		if !strings.Contains(err.Error(), Layout) {
			t.Fatalf("FromPath(%q) error %q does not point to layout %s", test.path, err, Layout)
		}
	}
}

func TestOfUsesDescriptorPath(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("wallet_service/wallet_service.proto"),
		Package: proto.String("wallet_service"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/wallet_service_pb;wallet_service_pb")},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{Name: proto.String("WalletService")},
			{Name: proto.String("WalletInnerService")},
		},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	for index := range file.Services().Len() {
		got, err := Of(file.Services().Get(index))
		if err != nil || got != "wallet_service" {
			t.Fatalf("Of(%s) = %q, %v, want wallet_service", file.Services().Get(index).FullName(), got, err)
		}
	}
	if _, err := Of(nil); err == nil {
		t.Fatal("Of(nil) succeeded")
	}
}

func TestOfReportsServiceInVersionError(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("orders/v1/order.proto"),
		Package: proto.String("orders.v1"),
		Syntax:  proto.String("proto3"),
		Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("OrderService")}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Of(file.Services().Get(0)); err == nil || !strings.Contains(err.Error(), "orders.v1.OrderService") {
		t.Fatalf("Of version error = %v, want service full name", err)
	}
}

func TestGoPackageNameMatchesProtocGenGo(t *testing.T) {
	tests := []struct {
		path, goPackage, want string
	}{
		{path: "a/b.proto", goPackage: "example.com/orders/v1;ordersv1", want: "ordersv1"},
		{path: "a/b.proto", goPackage: "example.com/orders-api", want: "orders_api"},
		{path: "a/b.proto", goPackage: "example.com/1orders", want: "_1orders"},
		{path: "a/b.proto", goPackage: "example.com/type", want: "_type"},
		{path: "a/my-file.proto", want: "my_file"},
	}
	for _, test := range tests {
		file := &descriptorpb.FileDescriptorProto{Name: proto.String(test.path), Syntax: proto.String("proto3")}
		if test.goPackage != "" {
			file.Options = &descriptorpb.FileOptions{GoPackage: proto.String(test.goPackage)}
		}
		descriptor, err := protodesc.NewFile(file, new(protoregistry.Files))
		if err != nil {
			t.Fatal(err)
		}
		if got := goPackageName(descriptor); got != test.want {
			t.Fatalf("goPackageName(%q, %q) = %q, want %q", test.path, test.goPackage, got, test.want)
		}
	}
}

func TestIsFrameworkExcludesStandardServices(t *testing.T) {
	health := grpc_health_v1.File_grpc_health_v1_health_proto.Services().Get(0)
	if !IsFramework(health.FullName()) {
		t.Fatalf("%s should be a framework service", health.FullName())
	}
	if !IsFramework("kratos.api.Metadata") {
		t.Fatal("kratos metadata service should be a framework service")
	}
	if IsFramework(protoreflect.FullName("wallet_service.WalletService")) {
		t.Fatal("business service reported as framework service")
	}
}
