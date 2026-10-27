package server

import (
	"context"
	nethttp "net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationapp "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var (
	_ foundationapp.ServiceNamer       = (*serviceRuntime)(nil)
	_ foundationapp.RegistrationExempt = (*managementRuntime)(nil)
)

// testMethod 描述测试 proto 中的一个方法；rule 为 nil 表示未声明 google.api.http。
type testMethod struct {
	name      string
	rule      *annotations.HttpRule
	streaming bool
}

// registerTestProto 在 files 中登记一个只含服务与空消息的 proto 文件。
func registerTestProto(t *testing.T, files *protoregistry.Files, filePath, pkg, service string, methods ...testMethod) {
	t.Helper()
	file := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(filePath),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String(service + "Empty")}},
	}
	serviceProto := &descriptorpb.ServiceDescriptorProto{Name: proto.String(service)}
	for _, method := range methods {
		methodProto := &descriptorpb.MethodDescriptorProto{
			Name:            proto.String(method.name),
			InputType:       proto.String("." + pkg + "." + service + "Empty"),
			OutputType:      proto.String("." + pkg + "." + service + "Empty"),
			ClientStreaming: proto.Bool(method.streaming),
			ServerStreaming: proto.Bool(method.streaming),
		}
		if method.rule != nil {
			methodProto.Options = new(descriptorpb.MethodOptions)
			proto.SetExtension(methodProto.Options, annotations.E_Http, method.rule)
		}
		serviceProto.Method = append(serviceProto.Method, methodProto)
	}
	file.Service = []*descriptorpb.ServiceDescriptorProto{serviceProto}
	descriptor, err := protodesc.NewFile(file, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.RegisterFile(descriptor); err != nil {
		t.Fatal(err)
	}
}

func get(path string) *annotations.HttpRule {
	return &annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: path}}
}

func post(path string) *annotations.HttpRule {
	return &annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: path}, Body: "*"}
}

func noopHandler(kratoshttp.Context) error { return nil }

func TestKratosRoutePathMatchesProtocGenGoHTTP(t *testing.T) {
	tests := map[string]string{
		"/wallet/{wallet_id}":             "/wallet/{wallet_id}",
		"/v1/{name=messages/*}":           "/v1/{name:messages/.*}",
		"/v1/{name=shelves/*/books/*}/x":  "/v1/{name:shelves/.*/books/.*}/x",
		"/v1/{ name = messages/* }":       "/v1/{name: messages/.* }",
		"/v1/{a=*}":                       "/v1/{a=*}",
		"/v1/{book.name=shelves/*}:check": "/v1/{book.name:shelves/.*}:check",
	}
	for input, want := range tests {
		if got := kratosRoutePath(input); got != want {
			t.Fatalf("kratosRoutePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHTTPRouteIndexFollowsGeneratorRules(t *testing.T) {
	files := new(protoregistry.Files)
	registerTestProto(t, files, "wallet_service/wallet.proto", "wallet_service", "WalletService",
		testMethod{name: "Get", rule: get("/wallet/{wallet_id}")},
		testMethod{name: "Update", rule: &annotations.HttpRule{
			Pattern:            &annotations.HttpRule_Put{Put: "/wallet/{wallet_id}"},
			AdditionalBindings: []*annotations.HttpRule{post("/wallet/{wallet_id}:update")},
		}},
		testMethod{name: "Custom", rule: &annotations.HttpRule{Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "HEAD", Path: "/wallet"}}}},
		testMethod{name: "Raw"},
		testMethod{name: "Join", rule: get("/mpc/join"), streaming: true},
		testMethod{name: "Stream", streaming: true},
	)
	index := newHTTPRouteIndex(files)

	for _, route := range []httpRoute{
		{method: nethttp.MethodGet, path: "/wallet/{wallet_id}"},
		{method: nethttp.MethodPut, path: "/wallet/{wallet_id}"},
		{method: nethttp.MethodPost, path: "/wallet/{wallet_id}:update"},
		{method: nethttp.MethodHead, path: "/wallet"},
		{method: nethttp.MethodPost, path: "/wallet_service.WalletService/Raw"},
		{method: nethttp.MethodGet, path: "/mpc/join"},
	} {
		if services := index.lookup(route.method, route.path); len(services) != 1 || services[0].FullName() != "wallet_service.WalletService" {
			t.Fatalf("lookup(%s %s) = %v", route.method, route.path, services)
		}
	}
	if services := index.lookup(nethttp.MethodPost, "/wallet_service.WalletService/Stream"); len(services) != 0 {
		t.Fatalf("streaming method without rule indexed: %v", services)
	}
}

func TestCollectHTTPServiceNamesMapsRoutesAndReportsUnmapped(t *testing.T) {
	files := new(protoregistry.Files)
	registerTestProto(t, files, "wallet_service/wallet.proto", "wallet_service", "WalletService",
		testMethod{name: "Get", rule: get("/wallet/{wallet_id}")},
		testMethod{name: "List", rule: get("/wallets")},
	)
	registerTestProto(t, files, "billing_service/billing.proto", "billing_service", "BillingService",
		testMethod{name: "List", rule: get("/wallets")},
		testMethod{name: "Bill", rule: post("/bills")},
	)
	registerTestProto(t, files, "orders/v1/order.proto", "orders.v1", "OrderService",
		testMethod{name: "Get", rule: get("/orders/{id}")},
	)
	index := newHTTPRouteIndex(files)

	srv := kratoshttp.NewServer(kratoshttp.PathPrefix("/api"))
	router := srv.Route("/")
	router.GET("/wallet/{wallet_id}", noopHandler)
	router.POST("/bills", noopHandler)
	router.GET("/wallets", noopHandler)
	router.GET("/orders/{id}", noopHandler)
	router.GET("/hand-written", noopHandler)

	names, byRoute, err := collectHTTPServiceNames(srv, "/api", true, index)
	if err != nil {
		t.Fatal(err)
	}
	got, unmapped, nameErr := names.ServiceNames()
	if !slices.Equal(got, []string{"billing_service", "wallet_service"}) {
		t.Fatalf("names = %v", got)
	}
	if len(unmapped) != 2 || !strings.Contains(unmapped[0], "GET /api/hand-written is not declared") || !strings.Contains(unmapped[1], "GET /api/wallets matches several services [billing_service wallet_service]") {
		t.Fatalf("unmapped = %q", unmapped)
	}
	if nameErr == nil || !strings.Contains(nameErr.Error(), "orders.v1.OrderService") {
		t.Fatalf("version directory error = %v", nameErr)
	}
	if byRoute[httpRoute{method: nethttp.MethodGet, path: "/api/wallet/{wallet_id}"}] != "wallet_service" {
		t.Fatalf("route names = %v", byRoute)
	}
}

func TestCollectHTTPServiceNamesReportsNativeOnlyRegistrations(t *testing.T) {
	index := newHTTPRouteIndex(new(protoregistry.Files))
	native := kratoshttp.NewServer()
	native.HandleFunc("/native", func(nethttp.ResponseWriter, *nethttp.Request) {})

	names, _, err := collectHTTPServiceNames(native, "", true, index)
	if err != nil {
		t.Fatal(err)
	}
	if got, unmapped, _ := names.ServiceNames(); len(got) != 0 || len(unmapped) != 1 || !strings.Contains(unmapped[0], "native Kratos APIs") {
		t.Fatalf("native-only names = %v, unmapped = %q", got, unmapped)
	}

	empty, _, err := collectHTTPServiceNames(kratoshttp.NewServer(), "", false, index)
	if err != nil {
		t.Fatal(err)
	}
	if got, unmapped, nameErr := empty.ServiceNames(); len(got) != 0 || len(unmapped) != 0 || nameErr != nil {
		t.Fatalf("empty server names = %v, %q, %v", got, unmapped, nameErr)
	}
}

func TestStripPathPrefix(t *testing.T) {
	tests := []struct{ path, prefix, want string }{
		{path: "/api/wallets", prefix: "/api", want: "/wallets"},
		{path: "/api/wallets", prefix: "/api/", want: "/wallets"},
		{path: "/api", prefix: "/api", want: "/"},
		{path: "/apiary/wallets", prefix: "/api", want: "/apiary/wallets"},
		{path: "/wallets", prefix: "", want: "/wallets"},
	}
	for _, test := range tests {
		if got := stripPathPrefix(test.path, test.prefix); got != test.want {
			t.Fatalf("stripPathPrefix(%q, %q) = %q, want %q", test.path, test.prefix, got, test.want)
		}
	}
}

type anyService interface{}

type anyServiceImpl struct{}

func registerTestGRPCService(srv *grpc.Server, name string) {
	srv.RegisterService(&grpc.ServiceDesc{ServiceName: name, HandlerType: (*anyService)(nil)}, anyServiceImpl{})
}

func TestCollectGRPCServiceNamesSkipsFrameworkServices(t *testing.T) {
	files := new(protoregistry.Files)
	registerTestProto(t, files, "wallet_service/wallet.proto", "wallet_service", "WalletService", testMethod{name: "Get"})
	registerTestProto(t, files, "wallet_service/wallet_inner.proto", "wallet_service", "WalletInnerService", testMethod{name: "Get"})
	registerTestProto(t, files, "orders/v1/order.proto", "orders.v1", "OrderService", testMethod{name: "Get"})

	srv := kratosgrpc.NewServer()
	registerTestGRPCService(srv.Server, "wallet_service.WalletService")
	registerTestGRPCService(srv.Server, "wallet_service.WalletInnerService")
	registerTestGRPCService(srv.Server, "legacy.HandWrittenService")
	registerTestGRPCService(srv.Server, "orders.v1.OrderService")
	// Kratos 自动注册的健康检查、反射与 kratos.api.Metadata 都应被跳过。
	for _, framework := range []string{"grpc.health.v1.Health", "kratos.api.Metadata"} {
		if _, ok := srv.GetServiceInfo()[framework]; !ok {
			t.Fatalf("fixture expects Kratos to register %s", framework)
		}
	}

	names, byService := collectGRPCServiceNames(srv, files)
	got, unmapped, err := names.ServiceNames()
	if !slices.Equal(got, []string{"wallet_service"}) {
		t.Fatalf("names = %v", got)
	}
	if len(unmapped) != 1 || !strings.Contains(unmapped[0], "legacy.HandWrittenService") {
		t.Fatalf("unmapped = %q", unmapped)
	}
	if err == nil || !strings.Contains(err.Error(), "orders.v1.OrderService") {
		t.Fatalf("version directory error = %v", err)
	}
	if byService["wallet_service.WalletInnerService"] != "wallet_service" {
		t.Fatalf("service names = %v", byService)
	}
	for _, framework := range []string{"grpc.health.v1.Health", "kratos.api.Metadata"} {
		if _, ok := byService[framework]; ok {
			t.Fatalf("framework service %s mapped to a business name", framework)
		}
	}
}

// NewRuntime 返回的业务 Server 汇报服务名并保留端点与回滚能力，管理端口声明不参与服务注册。
func TestRuntimeServersReportServiceNames(t *testing.T) {
	registerTestProto(t, protoregistry.GlobalFiles, "runtime_names_service/runtime.proto", "runtime_names_service", "RuntimeNamesService",
		testMethod{name: "Get", rule: get("/runtime-names/{id}")},
	)
	spec := NewSpec()
	spec.HTTP().Register(
		HandleRoute(nethttp.MethodGet, "/runtime-names/{id}", "", func(context.Context, *HTTPRequest) (any, error) { return nil, nil }),
		HandleRoute(nethttp.MethodGet, "/undeclared", "", func(context.Context, *HTTPRequest) (any, error) { return nil, nil }),
	)
	spec.GRPC().Register(func(srv GRPCServer) error {
		registerTestGRPCService(srv.Server, "runtime_names_service.RuntimeNamesService")
		return nil
	})
	config := proto.CloneOf(defaultConfig)
	config.Http.Addr, config.Grpc.Addr = proto.String("127.0.0.1:0"), proto.String("127.0.0.1:0")
	// 管理地址与业务地址字面不同，才会独立监听而不是复用业务端口。
	config.Http.Metrics.Addr = proto.String("localhost:0")
	config.Http.Health.Addr = proto.String("localhost:0")
	runtime, cleanup, err := NewRuntime(testconfig.New(t, "server", config), newRuntimeTestLogger(t),
		testMetricsProvider{registry: prometheus.NewRegistry()},
		runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	httpServer, grpcServer := runtime.Servers()
	for transport, server := range map[string]any{"http": httpServer, "grpc": grpcServer} {
		namer, ok := server.(foundationapp.ServiceNamer)
		if !ok {
			t.Fatalf("%s server %T does not report service names", transport, server)
		}
		names, unmapped, err := namer.ServiceNames()
		if err != nil || !slices.Equal(names, []string{"runtime_names_service"}) {
			t.Fatalf("%s names = %v, %v", transport, names, err)
		}
		if transport == "http" && (len(unmapped) != 1 || !strings.Contains(unmapped[0], "/undeclared")) {
			t.Fatalf("http unmapped = %q", unmapped)
		}
		if transport == "grpc" && len(unmapped) != 0 {
			t.Fatalf("grpc unmapped = %q", unmapped)
		}
		if _, ok := server.(interface{ Endpoint() (*url.URL, error) }); !ok {
			t.Fatalf("%s server lost Endpointer", transport)
		}
		if _, ok := server.(interface{ AbortStartup(context.Context) error }); !ok {
			t.Fatalf("%s server lost AbortStartup", transport)
		}
	}
	management := runtime.ManagementServers()
	if len(management) == 0 {
		t.Fatal("expected a management server")
	}
	for _, server := range management {
		exempt, ok := server.(foundationapp.RegistrationExempt)
		if !ok || !exempt.RegistrationExempt() {
			t.Fatalf("management server %T is not registration exempt", server)
		}
	}
}
