package logging

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestDeadlineFieldsRecordSelectedLimits(t *testing.T) {
	store, err := deadline.NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(2 * time.Second),
		MaxTimeout:      durationpb.New(800 * time.Millisecond),
		MinBudget:       durationpb.New(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := store.Derive(context.Background(), "/logging.Test/Call")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	fields := deadlineFields()
	values := make(map[string]any, len(fields)/2)
	for index := 0; index < len(fields); index += 2 {
		key := fields[index].(string)
		valuer := fields[index+1].(kratoslog.Valuer)
		values[key] = valuer(ctx)
	}

	if got := values["deadline.source"]; got != deadline.SourceMax {
		t.Fatalf("deadline.source = %#v", got)
	}
	if got := values["deadline.fallback_ms"]; got != int64(2000) {
		t.Fatalf("deadline.fallback_ms = %#v", got)
	}
	if got := values["deadline.max_ms"]; got != int64(800) {
		t.Fatalf("deadline.max_ms = %#v", got)
	}
	if got := values["deadline.min_budget_ms"]; got != int64(20) {
		t.Fatalf("deadline.min_budget_ms = %#v", got)
	}
	if got := values["deadline.remaining_ms"]; got == nil {
		t.Fatal("deadline.remaining_ms is nil")
	}
}

func TestDeadlineFieldsReturnNilWithoutBudget(t *testing.T) {
	for index := 1; index < len(deadlineFields()); index += 2 {
		valuer := deadlineFields()[index].(kratoslog.Valuer)
		if got := valuer(context.Background()); got != nil {
			t.Fatalf("deadline field %d without budget = %#v", index/2, got)
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
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
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
