package app

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
)

// namedTestServer 是汇报服务名的可注册 Runtime。
type namedTestServer struct {
	endpointTestServer
	names, unmapped []string
	err             error
}

func (s *namedTestServer) ServiceNames() ([]string, []string, error) {
	return s.names, s.unmapped, s.err
}

// exemptTestServer 公布端点但声明不参与服务注册。
type exemptTestServer struct{ endpointTestServer }

func (*exemptTestServer) RegistrationExempt() bool { return true }

func testEndpoint(scheme, host string) endpointTestServer {
	return endpointTestServer{endpoint: &url.URL{Scheme: scheme, Host: host}}
}

// newInstancesTestApp 构造按给定 Runtime 解析实例的 App。
func newInstancesTestApp(t *testing.T, runtimes ...Runtime) *App {
	t.Helper()
	application := newTestApp(t, appSnapshot{runtimes: runtimes}, newStaticStopPolicy(time.Second))
	application.id, application.name, application.version = "proc-1", "app-name", "v1"
	application.metadata = map[string]string{"env": "dev"}
	application.registrar = new(registrarCallFake)
	application.registrarTimeout = 100 * time.Millisecond
	return application
}

// registrationTestRuntimes 覆盖 HTTP/gRPC 业务 Server、未汇报服务名的自定义 Runtime、管理端口、
// 无业务服务的 Server 和没有端点的 Runtime。
func registrationTestRuntimes() []Runtime {
	return []Runtime{
		&namedTestServer{endpointTestServer: testEndpoint("http", "10.0.0.1:8000"), names: []string{"orders"}, unmapped: []string{"http route POST /hook is not declared in proto"}},
		&namedTestServer{endpointTestServer: testEndpoint("grpc", "10.0.0.1:9000"), names: []string{"billing", "orders"}},
		&endpointTestServer{endpoint: &url.URL{Scheme: "tcp", Host: "10.0.0.1:7000"}},
		&exemptTestServer{endpointTestServer: testEndpoint("http", "10.0.0.1:8001")},
		&namedTestServer{endpointTestServer: testEndpoint("http", "10.0.0.1:8100")},
		new(testServer),
	}
}

func instanceSummary(instances []*registry.ServiceInstance) map[string][]string {
	summary := make(map[string][]string, len(instances))
	for _, instance := range instances {
		summary[instance.ID+" "+instance.Name] = instance.Endpoints
	}
	return summary
}

func TestBuildInstancesGroupsEndpointsByServiceName(t *testing.T) {
	application := newInstancesTestApp(t, registrationTestRuntimes()...)
	instances, err := application.buildInstances()
	if err != nil {
		t.Fatal(err)
	}
	// 未映射的业务入口与未汇报服务名的 Runtime 归入应用名；管理端口与无业务服务的 Server 不注册。
	want := map[string][]string{
		"proc-1 app-name":        {"http://10.0.0.1:8000", "tcp://10.0.0.1:7000"},
		"proc-1@billing billing": {"grpc://10.0.0.1:9000"},
		"proc-1@orders orders":   {"http://10.0.0.1:8000", "grpc://10.0.0.1:9000"},
	}
	if got := instanceSummary(instances); !reflect.DeepEqual(got, want) {
		t.Fatalf("instances = %v, want %v", got, want)
	}
	if instances[0].Name != "app-name" {
		t.Fatalf("application instance should register first: %v", instances[0].Name)
	}
	for _, instance := range instances {
		if instance.Metadata[MetadataAppName] != "app-name" || instance.Metadata[MetadataProcessID] != "proc-1" || instance.Metadata["env"] != "dev" || instance.Version != "v1" {
			t.Fatalf("%s metadata = %v version = %q", instance.Name, instance.Metadata, instance.Version)
		}
	}
	if _, ok := application.metadata[MetadataAppName]; ok {
		t.Fatal("instance metadata leaked into application metadata")
	}
	if got := application.Endpoint(); len(got) != 4 {
		t.Fatalf("Endpoint = %v, want all registrable endpoints", got)
	}
}

func TestBuildInstancesSkipsProcessesWithoutBusinessServices(t *testing.T) {
	application := newInstancesTestApp(t,
		&namedTestServer{endpointTestServer: testEndpoint("http", "10.0.0.1:8000")},
		&exemptTestServer{endpointTestServer: testEndpoint("http", "10.0.0.1:8001")},
		new(testServer),
	)
	instances, err := application.buildInstances()
	if err != nil || len(instances) != 0 {
		t.Fatalf("instances = %v, %v, want none", instanceSummary(instances), err)
	}
}

func TestBuildInstancesFailsOnNamingErrors(t *testing.T) {
	namingErr := errors.New(`derived service name "v1" is an API version`)
	application := newInstancesTestApp(t, &namedTestServer{endpointTestServer: testEndpoint("grpc", "10.0.0.1:9000"), names: []string{"orders"}, err: namingErr})
	if _, err := application.buildInstances(); !errors.Is(err, namingErr) {
		t.Fatalf("error = %v, want naming error", err)
	}
}

func TestBuildInstancesWithoutRegistrarOnlyResolvesEndpoints(t *testing.T) {
	application := newInstancesTestApp(t, registrationTestRuntimes()...)
	application.registrar = nil
	instances, err := application.buildInstances()
	if err != nil || instances != nil {
		t.Fatalf("instances = %v, %v, want nil", instances, err)
	}
	if got := application.Endpoint(); len(got) != 4 {
		t.Fatalf("Endpoint = %v", got)
	}
}

func TestBuildInstancesReturnsEndpointErrors(t *testing.T) {
	endpointErr := errors.New("listener not prepared")
	application := newInstancesTestApp(t, &endpointTestServer{endpointErr: endpointErr})
	if _, err := application.buildInstances(); !errors.Is(err, endpointErr) {
		t.Fatalf("error = %v, want endpoint error", err)
	}
}

func testInstances(names ...string) []*registry.ServiceInstance {
	instances := make([]*registry.ServiceInstance, 0, len(names))
	for _, name := range names {
		instances = append(instances, &registry.ServiceInstance{ID: "proc-1@" + name, Name: name})
	}
	return instances
}

// 任一实例注册失败时注销已注册的实例，不在注册中心残留部分名字。
func TestRegisterRollsBackRegisteredInstancesOnFailure(t *testing.T) {
	registerErr := errors.New("register billing failed")
	var deregistered []string
	registrar := &registrarCallFake{
		registerFn: func(_ context.Context, instance *registry.ServiceInstance) error {
			if instance.Name == "billing" {
				return registerErr
			}
			return nil
		},
		deregisterFn: func(_ context.Context, instance *registry.ServiceInstance) error {
			deregistered = append(deregistered, instance.Name)
			return nil
		},
	}
	application := newRegistrarTestApp(t, registrar, appSnapshot{})
	if err := application.register(testInstances("orders", "billing", "users")); !errors.Is(err, registerErr) {
		t.Fatalf("register error = %v", err)
	}
	if !reflect.DeepEqual(deregistered, []string{"orders"}) {
		t.Fatalf("rolled back %v, want orders", deregistered)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if registerCalls, deregisterCalls := registrar.calls(); registerCalls != 2 || deregisterCalls != 1 {
		t.Fatalf("calls = register %d, deregister %d", registerCalls, deregisterCalls)
	}
}

// 停止请求在两次注册之间到达时，已注册与之后注册的实例都只注销一次。
func TestRegisterDeregistersEveryInstanceOnceWhenStoppingMidway(t *testing.T) {
	var deregistered []string
	var application *App
	registrar := &registrarCallFake{
		registerFn: func(_ context.Context, instance *registry.ServiceInstance) error {
			if instance.Name == "orders" {
				go func() { _ = application.Stop() }()
				<-application.stoppingSignal
			}
			return nil
		},
		deregisterFn: func(_ context.Context, instance *registry.ServiceInstance) error {
			deregistered = append(deregistered, instance.Name)
			return nil
		},
	}
	application = newRegistrarTestApp(t, registrar, appSnapshot{})
	if err := application.register(testInstances("orders", "billing")); !errors.Is(err, errAppStopping) {
		t.Fatalf("register error = %v, want stopping", err)
	}
	if err := application.Stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(deregistered, ",") != "orders" {
		t.Fatalf("deregistered = %v, want only the registered instance once", deregistered)
	}
}

func TestDeregisterJoinsErrorsPerInstance(t *testing.T) {
	failure := errors.New("consul unavailable")
	registrar := &registrarCallFake{deregisterFn: func(context.Context, *registry.ServiceInstance) error { return failure }}
	application := newRegistrarTestApp(t, registrar, appSnapshot{})
	if err := application.register(testInstances("orders", "billing")); err != nil {
		t.Fatal(err)
	}
	application.deregister()
	application.deregister()
	err := application.deregisterErr
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "deregister orders") || !strings.Contains(err.Error(), "deregister billing") {
		t.Fatalf("deregister error = %v", err)
	}
	if _, deregisterCalls := registrar.calls(); deregisterCalls != 2 {
		t.Fatalf("deregister calls = %d, want 2", deregisterCalls)
	}
}
