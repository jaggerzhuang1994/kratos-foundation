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
}
