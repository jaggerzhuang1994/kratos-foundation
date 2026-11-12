package server

import (
	"context"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	kratostracing "github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// tracedPanicTransport 提供 tracing 与恢复日志实际读取的传输信息。
type tracedPanicTransport struct {
	transport.Transporter
	header tracedPanicHeader
}

func (tracedPanicTransport) Kind() transport.Kind              { return transport.KindGRPC }
func (tracedPanicTransport) Endpoint() string                  { return "grpc://localhost:9000" }
func (tracedPanicTransport) Operation() string                 { return "/test/Call" }
func (t tracedPanicTransport) RequestHeader() transport.Header { return t.header }
func (t tracedPanicTransport) ReplyHeader() transport.Header   { return t.header }

type tracedPanicHeader map[string][]string

func (h tracedPanicHeader) Get(key string) string {
	if values := h[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}
func (h tracedPanicHeader) Set(key, value string)      { h[key] = []string{value} }
func (h tracedPanicHeader) Add(key, value string)      { h[key] = append(h[key], value) }
func (h tracedPanicHeader) Values(key string) []string { return h[key] }
func (h tracedPanicHeader) Keys() []string {
	keys := []string{}
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

// 业务 panic 在访问日志之内的恢复层处理：恢复日志只记一次并带本次请求的 trace.id；
// 恢复出的 500 仍有一条访问日志，不再重复记录 server.request.failed。
func TestHandlerPanicIsRecoveredInsideTracing(t *testing.T) {
	logger, path := newBoundaryTestLogger(t)
	// 与 bootstrap 相同：trace.id 由 Context 中的 span 求值。
	logger = logger.With(log.TraceIDKey, kratostracing.TraceID())
	config, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	policies, cancel, err := newMiddlewarePolicies(newMiddlewarePolicyManager(t), logger, config,
		testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)

	handler := middleware.Chain(newMiddlewares(policies).build(nil)...)(func(context.Context, any) (any, error) {
		var missing *struct{ value string }
		return missing.value, nil
	})
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	ctx := transport.NewServerContext(context.Background(), tracedPanicTransport{header: tracedPanicHeader{
		"traceparent": {"00-" + traceID + "-00f067aa0ba902b7-01"},
	}})
	before := logSize(t, path)
	reply, err := handler(ctx, nil)
	if reply != nil || foundationerrors.Code(err) != 500 {
		t.Fatalf("reply=%v err=%v", reply, err)
	}
	logs := logDelta(t, path, before)
	if strings.Count(logs, "event=server.request.panic.recovered") != 1 {
		t.Fatalf("panic must be logged exactly once: %s", logs)
	}
	for _, field := range []string{"trace.id=" + traceID, "panic_type=runtime.errorString", "nil pointer dereference", "stack=", "operation=/test/Call"} {
		if !strings.Contains(logs, field) {
			t.Fatalf("panic diagnostic missing %q: %s", field, logs)
		}
	}
	if strings.Contains(logs, "event=server.request.failed") {
		t.Fatalf("panic must not be logged again as a request failure: %s", logs)
	}
	accessLogs := 0
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, "event=server.request.completed") {
			accessLogs++
			if !strings.Contains(line, "code=500") || !strings.Contains(line, "trace.id="+traceID) {
				t.Fatalf("access log of panicked request lacks code or trace: %s", line)
			}
		}
	}
	if accessLogs != 1 {
		t.Fatalf("panicked request access logs = %d, want 1: %s", accessLogs, logs)
	}
}

// panic 恢复日志不受日志级别限制：实例级别与输出级别都调到 FATAL 时，普通 ERROR 被过滤，panic 仍然输出。
func TestPanicRecoveryIsLoggedRegardlessOfLevel(t *testing.T) {
	logger, path := newBoundaryTestLogger(t, kratoslog.LevelFatal)
	// 线上配置常把 event 放进 filter_keys；事件名不输出也不影响放行。
	logger = logger.WithFilterKeys("event")
	logger.With("event", "server.request.failed").Error("ordinary failure")
	if size := logSize(t, path); size != 0 {
		t.Fatalf("ordinary error must be filtered at FATAL level, got %d bytes", size)
	}
	_, err := recoverRequests(logger)(func(context.Context, any) (any, error) {
		panic("boom")
	})(context.Background(), nil)
	if foundationerrors.Code(err) != 500 {
		t.Fatalf("err = %v", err)
	}
	line := logDelta(t, path, 0)
	for _, field := range []string{"request panic recovered", "panic=boom", "stack="} {
		if !strings.Contains(line, field) {
			t.Fatalf("panic log missing %q at FATAL level: %s", field, line)
		}
	}
}
