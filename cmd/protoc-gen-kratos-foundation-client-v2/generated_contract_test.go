package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestGeneratedClientsCompileAndReleaseLeasesAgainstPublicFactory 编译并执行生成代码，
// 防止模板继续引用已经删除的公共 API，或遗漏任一调用分支的租约释放。
func TestGeneratedClientsCompileAndReleaseLeasesAgainstPublicFactory(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const foundationModule = "github.com/jaggerzhuang1994/kratos-foundation/v2"
	moduleText := strings.Replace(string(module), "module "+foundationModule, "module example.com/generated-client-contract", 1)
	moduleText += "\nrequire " + foundationModule + " v2.0.0\nreplace " + foundationModule + " => " + strconv.Quote(root) + "\n"
	writeGeneratedFixture(t, filepath.Join(directory, "go.mod"), moduleText)
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedFixture(t, filepath.Join(directory, "go.sum"), string(sums))
	for _, annotated := range []bool{false, true} {
		name := "grpc"
		if annotated {
			name = "http"
		}
		packageDirectory := filepath.Join(directory, name)
		if err := os.Mkdir(packageDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		fixture := newClientGeneratorFixture(t, false)
		if annotated {
			method := fixture.Request.ProtoFile[0].Service[0].Method[0]
			method.Options = new(descriptorpb.MethodOptions)
			proto.SetExtension(method.Options, annotations.E_Http, &annotations.HttpRule{
				Pattern: &annotations.HttpRule_Get{Get: "/orders"},
			})
			fixture, err = (protogen.Options{}).New(fixture.Request)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := run(fixture); err != nil {
			t.Fatal(err)
		}
		writeGeneratedFixture(t, filepath.Join(packageDirectory, "order_client.pb.go"), fixture.Response().File[0].GetContent())
		writeGeneratedFixture(t, filepath.Join(packageDirectory, "contract_test.go"),
			strings.Replace(generatedClientContract, "HTTP_ANNOTATED", strconv.FormatBool(annotated), 1))
	}
	// 子模块编译和测试也受总预算限制，避免业务门禁遗留永久阻塞的子进程。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout=60s", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated client contract failed: %v\n%s", err, output)
	}
}

func writeGeneratedFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 消息和协议客户端只替代网络边界；生成适配器使用仓库真实的 client.Factory 契约。
const generatedClientContract = `package ordersv1

import (
    "context"
    "errors"
    "strings"
    "reflect"
    "net"
    nethttp "net/http"
    "net/http/httptest"
    "time"
    "testing"

    kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/health"
    healthpb "google.golang.org/grpc/health/grpc_health_v1"
    "google.golang.org/grpc/status"
    "google.golang.org/grpc/test/bufconn"
)

const httpAnnotated = HTTP_ANNOTATED

type GetRequest struct{ Network bool }
type GetReply struct { Protocol string }
type grpcClient struct{ conn *grpc.ClientConn }
type httpClient struct{ conn *kratoshttp.Client }

func NewOrderServiceClient(conn *grpc.ClientConn) grpcClient { return grpcClient{conn} }
func (c grpcClient) Get(ctx context.Context, request *GetRequest, opts ...grpc.CallOption) (*GetReply, error) {
    if !reflect.DeepEqual(opts, client.GRPCCallOptionsFromContext(ctx)) { return nil, errors.New("grpc options not forwarded") }
    if request.Network {
        _, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{}, opts...)
        if err != nil { return nil, err }
    }
    return &GetReply{Protocol: "grpc"}, nil
}
func NewOrderServiceHTTPClient(conn *kratoshttp.Client) httpClient { return httpClient{conn} }
func (c httpClient) Get(ctx context.Context, request *GetRequest, opts ...kratoshttp.CallOption) (*GetReply, error) {
    if !reflect.DeepEqual(opts, client.HTTPCallOptionsFromContext(ctx)) { return nil, errors.New("http options not forwarded") }
    if request.Network {
        var reply GetReply
        err := c.conn.Invoke(ctx, "GET", "/orders", nil, &reply, opts...)
        return &reply, err
    }
    return &GetReply{Protocol: "http"}, nil
}

type recordingFactory struct {
    name string
    ctx context.Context
    releases int
    http *kratoshttp.Client
    grpc *grpc.ClientConn
    err error
}

var _ client.Factory = (*recordingFactory)(nil)

func (f *recordingFactory) AcquireClient(ctx context.Context, name string) (*kratoshttp.Client, *grpc.ClientConn, func(), error) {
    f.ctx, f.name = ctx, name
    if f.err != nil { return nil, nil, nil, f.err }
    return f.http, f.grpc, func() { f.releases++ }, nil
}

func TestExplicitConnectionAndLeaseLifetime(t *testing.T) {
    sentinel := errors.New("connection unavailable")
    for _, test := range []struct {
        name string
        factory recordingFactory
        protocol string
        errorText string
        releases int
    }{
        {name: "acquire error", factory: recordingFactory{err: sentinel}, errorText: "connection unavailable"},
        {name: "empty result", errorText: "neither an HTTP nor a gRPC", releases: 1},
        {name: "grpc", factory: recordingFactory{grpc: new(grpc.ClientConn)}, protocol: "grpc", releases: 1},
        {name: "http", factory: recordingFactory{http: new(kratoshttp.Client)}, protocol: "http", releases: 1},
    } {
        t.Run(test.name, func(t *testing.T) {
            factory := &test.factory
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            ctx = client.WithGRPCCallOptions(ctx, grpc.WaitForReady(true))
            ctx = client.WithHTTPCallOptions(ctx, kratoshttp.Operation("GetOrder"))
            response, err := NewOrderServiceWithConnName(factory, "orders-eu").Get(ctx, new(GetRequest))
            expectedError := test.errorText
            if test.protocol == "http" && !httpAnnotated {
                expectedError = "google.api.http annotation is required"
            }
            if expectedError != "" {
                if err == nil || !strings.Contains(err.Error(), expectedError) { t.Fatalf("error = %v", err) }
            } else if err != nil || response == nil || response.Protocol != test.protocol {
                t.Fatalf("response=%v error=%v", response, err)
            }
            if test.factory.err != nil && !errors.Is(err, sentinel) { t.Fatalf("lost cause: %v", err) }
            if factory.name != "orders-eu" || factory.ctx != ctx { t.Fatalf("name/context not forwarded: %q", factory.name) }
            if factory.releases != test.releases { t.Fatalf("release calls=%d want=%d", factory.releases, test.releases) }
        })
    }
    factory := &recordingFactory{err: sentinel}
    _, _ = NewOrderService(factory).Get(context.Background(), new(GetRequest))
    if factory.name != "v1" { t.Fatalf("default connection=%q", factory.name) }
    if _, err := NewOrderService(nil).Get(context.Background(), new(GetRequest)); err == nil {
        t.Fatal("nil factory accepted")
    }
}

func TestGRPCCallOptionsAffectRealRPC(t *testing.T) {
    listener := bufconn.Listen(1<<20)
    server := grpc.NewServer()
    healthpb.RegisterHealthServer(server, health.NewServer())
    done := make(chan error, 1)
    go func() { done <- server.Serve(listener) }()
    t.Cleanup(func() {
        server.Stop()
        select { case err := <-done: if err != nil { t.Error(err) }; case <-time.After(time.Second): t.Error("grpc server did not stop") }
    })
    conn, err := grpc.NewClient("passthrough:///contract", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { if err := conn.Close(); err != nil { t.Error(err) } })
    factory := &recordingFactory{grpc: conn}
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _, err = NewOrderService(factory).Get(ctx, &GetRequest{Network: true})
    if err != nil { t.Fatal(err) }
    ctx = client.WithGRPCCallOptions(ctx, grpc.MaxCallRecvMsgSize(1))
    _, err = NewOrderService(factory).Get(ctx, &GetRequest{Network: true})
    if status.Code(err) != codes.ResourceExhausted || factory.releases != 2 { t.Fatalf("limit err=%v releases=%d", err, factory.releases) }
}

func TestHTTPCallOptionsRetrieveRealResponseHeaders(t *testing.T) {
    if !httpAnnotated { t.Skip("HTTP annotation intentionally absent") }
    server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-Contract", "forwarded")
        if _, err := w.Write([]byte("{\"Protocol\":\"http\"}")); err != nil { t.Error(err) }
    }))
    t.Cleanup(server.Close)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    conn, err := kratoshttp.NewClient(ctx, kratoshttp.WithEndpoint(server.URL))
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { if err := conn.Close(); err != nil { t.Error(err) } })
    factory := &recordingFactory{http: conn}
    var header nethttp.Header
    ctx = client.WithHTTPCallOptions(ctx, kratoshttp.Header(&header))
    response, err := NewOrderService(factory).Get(ctx, &GetRequest{Network: true})
    if err != nil || response.Protocol != "http" || header.Get("X-Contract") != "forwarded" || factory.releases != 1 { t.Fatalf("response=%v err=%v header=%v releases=%d", response, err, header, factory.releases) }
}

`
