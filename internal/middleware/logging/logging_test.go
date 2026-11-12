package logging

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestDeadlineFieldsDeclareDiagnosticKeys(t *testing.T) {
	fields := deadlineFields()
	if len(fields) != 4 || fields[0] != "deadline.source" || fields[2] != "deadline.remaining" {
		t.Fatalf("deadline fields = %#v", fields)
	}
}

func TestDeadlineDiagnosticsStayEmptyWithoutBudget(t *testing.T) {
	logger, path := newTestLogger(t)
	middleware := Client(logger, nil)
	ctx := request.WithDebug(context.Background())
	if _, err := middleware(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "deadline.source=") || strings.Contains(string(data), "deadline.remaining=") {
		t.Fatalf("deadline diagnostics without budget: %s", data)
	}
}

func TestDeadlineDiagnosticsOnlyAppearForRequestDebug(t *testing.T) {
	store, err := deadline.NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	logger, path := newTestLogger(t)
	middleware := Client(logger, nil)
	for index, debug := range []bool{false, true} {
		ctx, cancel, err := store.Derive(context.Background(), "/logging.Test/Call")
		if err != nil {
			t.Fatal(err)
		}
		if debug {
			ctx = request.WithDebug(ctx)
		}
		if _, err := middleware(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		line := lines[len(lines)-1]
		for _, field := range []string{"deadline.source=", "deadline.remaining="} {
			if got := strings.Contains(line, field); got != debug {
				t.Fatalf("case %d debug=%v %s present=%v: %s", index, debug, field, got, line)
			}
		}
	}
}

func TestMiddlewareDisableAndTransportBranchesPreserveHandlerContract(t *testing.T) {
	disabled := true
	if Server(nil, &config_pb.Middleware_Logging{Disable: &disabled}) != nil {
		t.Fatal("disabled server logging returned middleware")
	}
	if Client(nil, &config_pb.Middleware_Logging{Disable: &disabled}) != nil {
		t.Fatal("disabled client logging returned middleware")
	}

	logger, logPath := newTestLogger(t)
	wantErr := errors.New("handler failed")
	server := Server(logger, nil)
	client := Client(logger, nil)
	if server == nil || client == nil {
		t.Fatal("enabled logging returned nil middleware")
	}

	tests := []struct {
		name    string
		wrap    middleware.Middleware
		ctx     context.Context
		wantLog bool
	}{
		{name: "server without transport", wrap: server, ctx: context.Background(), wantLog: true},
		{
			name: "server websocket upgrade",
			wrap: server,
			ctx: transport.NewServerContext(
				context.Background(),
				newWebSocketTransport(),
			),
		},
		{name: "client", wrap: client, ctx: context.Background(), wantLog: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			called := 0
			response, err := test.wrap(middleware.Handler(func(context.Context, any) (any, error) {
				called++
				return "response", wantErr
			}))(test.ctx, "request")
			if called != 1 || response != "response" || !errors.Is(err, wantErr) {
				t.Fatalf("middleware result = (%v, %v), calls=%d", response, err, called)
			}
			after, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantLog && len(after) <= len(before) {
				t.Fatal("enabled middleware did not write an access log")
			}
			if test.wantLog && !strings.Contains(string(after[len(before):]), "caller=logging/logging.go:") {
				t.Fatalf("access log caller did not identify the middleware: %s", after[len(before):])
			}
			if !test.wantLog && len(after) != len(before) {
				t.Fatalf("WebSocket request wrote %d unexpected log bytes", len(after)-len(before))
			}
		})
	}
}

type testHTTPTransport struct {
	request *http.Request
}

func newWebSocketTransport() *testHTTPTransport {
	request := httptest.NewRequest("GET", "/socket", nil)
	request.Header.Set("Connection", "upgrade")
	request.Header.Set("Upgrade", "websocket")
	return &testHTTPTransport{request: request}
}

func (*testHTTPTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (*testHTTPTransport) Endpoint() string                { return "http://localhost" }
func (*testHTTPTransport) Operation() string               { return "/socket" }
func (*testHTTPTransport) RequestHeader() transport.Header { return nil }
func (*testHTTPTransport) ReplyHeader() transport.Header   { return nil }
func (t *testHTTPTransport) Request() *http.Request        { return t.request }
func (*testHTTPTransport) PathTemplate() string            { return "/socket" }

func newTestLogger(t *testing.T) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "access.log")
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: true,
		TimeFormat:  time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared, path
}

// sensitiveRequest 的 String 不应被日志调用，防止完整认证材料被格式化。
type sensitiveRequest struct{}

func (sensitiveRequest) String() string { panic("request must not be formatted") }

func TestAccessLogsOmitBodiesAndErrorDetails(t *testing.T) {
	logger, path := newTestLogger(t)
	for _, wrap := range []middleware.Middleware{Server(logger, nil), Client(logger, nil)} {
		_, err := wrap(func(context.Context, any) (any, error) { return sensitiveRequest{}, errors.New("private-error-secret") })(context.Background(), sensitiveRequest{})
		if err == nil {
			t.Fatal("handler error lost")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-error-secret") || strings.Contains(string(data), "args=") || strings.Contains(string(data), "stack=") {
		t.Fatalf("access log disclosed details: %s", data)
	}
	if !strings.Contains(string(data), "code=500") {
		t.Fatalf("status missing: %s", data)
	}
	if strings.Count(string(data), "event=server.request.completed") != 1 || strings.Count(string(data), "event=client.request.completed") != 1 {
		t.Fatalf("access event missing: %s", data)
	}
	// 访问日志与错误边界日志共用 transport/duration 字段名，不再输出 kind/latency；无传输上下文时 transport 为空被过滤。
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, " duration=") || strings.Contains(line, " kind=") || strings.Contains(line, " latency=") || !strings.Contains(line, "msg=request completed") {
			t.Fatalf("access log fields = %s", line)
		}
	}
}

type testGRPCTransport struct {
	operation string
}

func (*testGRPCTransport) Kind() transport.Kind            { return transport.KindGRPC }
func (*testGRPCTransport) Endpoint() string                { return "grpc://localhost" }
func (t *testGRPCTransport) Operation() string             { return t.operation }
func (*testGRPCTransport) RequestHeader() transport.Header { return nil }
func (*testGRPCTransport) ReplyHeader() transport.Header   { return nil }

func TestServerFrameworkSuccessLogsAtDebug(t *testing.T) {
	logger, path := newTestLogger(t)
	server := Server(logger, nil)
	tests := []struct {
		name      string
		operation string
		err       error
		wantLog   bool
	}{
		{name: "health success", operation: "/grpc.health.v1.Health/Check"},
		{name: "health failure", operation: "/grpc.health.v1.Health/Check", err: errors.New("not serving"), wantLog: true},
		{name: "business success", operation: "/wallet_service.WalletService/Get", wantLog: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := transport.NewServerContext(context.Background(), &testGRPCTransport{operation: test.operation})
			_, _ = server(func(context.Context, any) (any, error) { return nil, test.err })(ctx, nil)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(after[len(before):]), "operation="+test.operation); got != test.wantLog {
				t.Fatalf("info access log written=%v, want %v: %s", got, test.wantLog, after[len(before):])
			}
		})
	}
}

// backgroundTransport 是带可写请求头的 gRPC 传输替身。
type backgroundTransport struct {
	testGRPCTransport
	header backgroundHeader
}

func (t *backgroundTransport) RequestHeader() transport.Header { return t.header }

type backgroundHeader map[string][]string

func (h backgroundHeader) Get(key string) string {
	if values := h[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}
func (h backgroundHeader) Set(key, value string)      { h[key] = []string{value} }
func (h backgroundHeader) Add(key, value string)      { h[key] = append(h[key], value) }
func (h backgroundHeader) Values(key string) []string { return h[key] }
func (h backgroundHeader) Keys() []string {
	keys := []string{}
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

// 后台周期性调用成功时两端的访问摘要都降为 Debug，标记随请求头传给下游；失败照常按 Info 记录。
func TestBackgroundCallsLogSuccessAtDebugOnBothSides(t *testing.T) {
	logger, path := newTestLogger(t)
	const operation = "/base_service.BaseService/GetCurrencyInfos"
	written := func(run func()) string {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		run()
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(after[len(before):])
	}
	failure := errors.New("upstream unavailable")

	// 客户端：标记的调用写入请求头，成功不记 Info，失败照常记录；未标记的调用不写头。
	client := Client(logger, nil)
	header := backgroundHeader{}
	marked := transport.NewClientContext(request.WithBackground(context.Background()), &backgroundTransport{testGRPCTransport: testGRPCTransport{operation: operation}, header: header})
	if logs := written(func() { _, _ = client(func(context.Context, any) (any, error) { return nil, nil })(marked, nil) }); logs != "" {
		t.Fatalf("background client success logged at info: %s", logs)
	}
	if got := header.Values(BackgroundHeader); len(got) != 1 || got[0] != "1" {
		t.Fatalf("background header = %v", got)
	}
	if logs := written(func() { _, _ = client(func(context.Context, any) (any, error) { return nil, failure })(marked, nil) }); !strings.Contains(logs, "operation="+operation) {
		t.Fatalf("background client failure not logged: %s", logs)
	}
	plainHeader := backgroundHeader{}
	plain := transport.NewClientContext(context.Background(), &backgroundTransport{testGRPCTransport: testGRPCTransport{operation: operation}, header: plainHeader})
	if logs := written(func() { _, _ = client(func(context.Context, any) (any, error) { return nil, nil })(plain, nil) }); !strings.Contains(logs, "operation="+operation) || len(plainHeader) != 0 {
		t.Fatalf("ordinary client call must log at info without the header: %s %v", logs, plainHeader)
	}

	// 服务端：收到单值 1 时成功不记 Info，并把标记带入处理上下文；失败、多值与其他取值照常记录。
	server := Server(logger, nil)
	for _, tc := range []struct {
		name    string
		values  []string
		err     error
		wantLog bool
	}{
		{"marked success", []string{"1"}, nil, false},
		{"marked failure", []string{"1"}, failure, true},
		{"duplicated header", []string{"1", "1"}, nil, true},
		{"other value", []string{"true"}, nil, true},
		{"unmarked", nil, nil, true},
	} {
		incoming := backgroundHeader{}
		if tc.values != nil {
			incoming[BackgroundHeader] = tc.values
		}
		ctx := transport.NewServerContext(context.Background(), &backgroundTransport{testGRPCTransport: testGRPCTransport{operation: operation}, header: incoming})
		handlerBackground := false
		logs := written(func() {
			_, _ = server(func(ctx context.Context, _ any) (any, error) {
				handlerBackground = request.IsBackground(ctx)
				return nil, tc.err
			})(ctx, nil)
		})
		if got := strings.Contains(logs, "operation="+operation); got != tc.wantLog {
			t.Fatalf("%s: info access log written=%v, want %v: %s", tc.name, got, tc.wantLog, logs)
		}
		if wantMark := len(tc.values) == 1 && tc.values[0] == "1"; handlerBackground != wantMark {
			t.Fatalf("%s: handler background=%v, want %v", tc.name, handlerBackground, wantMark)
		}
	}
}
