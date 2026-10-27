package main

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/servicename"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
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
		"NewOrderServiceWithConnName(factory, \"orders\")",
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

// 生成期写入的默认连接名必须与运行期 servicename.Of 对同一描述符推导的服务名一致，
// 否则服务端注册名与客户端解析名会再次分叉。请求经过一次序列化，模拟 protoc 传入的真实字节。
func TestGeneratedConnNameMatchesRuntimeServiceName(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{path: "orders/order.proto", want: "orders"},
		{path: "order.proto", want: "order"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := newClientGeneratorFixture(t, false).Request
			request.FileToGenerate = []string{test.path}
			request.ProtoFile[0].Name = proto.String(test.path)
			raw, err := proto.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(pluginpb.CodeGeneratorRequest)
			if err := proto.Unmarshal(raw, decoded); err != nil {
				t.Fatal(err)
			}
			plugin, err := (protogen.Options{}).New(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(plugin); err != nil {
				t.Fatal(err)
			}
			content := plugin.Response().File[0].GetContent()
			if !strings.Contains(content, `NewOrderServiceWithConnName(factory, "`+test.want+`")`) {
				t.Fatalf("generated connection name is not %q:\n%s", test.want, content)
			}

			runtimeFile, err := protodesc.NewFile(decoded.ProtoFile[0], new(protoregistry.Files))
			if err != nil {
				t.Fatal(err)
			}
			got, err := servicename.Of(runtimeFile.Services().Get(0))
			if err != nil || got != test.want {
				t.Fatalf("servicename.Of = %q, %v, want generated name %q", got, err, test.want)
			}
		})
	}
}

func TestRunRejectsVersionDirectoryAsServiceName(t *testing.T) {
	request := newClientGeneratorFixture(t, false).Request
	request.FileToGenerate = []string{"orders/v1/order.proto"}
	request.ProtoFile[0].Name = proto.String("orders/v1/order.proto")
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatal(err)
	}
	err = run(plugin)
	if err == nil || !strings.Contains(err.Error(), "API version") || !strings.Contains(err.Error(), servicename.Layout) {
		t.Fatalf("version directory error = %v", err)
	}
}

func newClientGeneratorFixture(t *testing.T, streaming bool) *protogen.Plugin {
	t.Helper()
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"orders/order.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("orders/order.proto"),
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
