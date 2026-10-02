package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/transport"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestDynamicMiddlewareReplacesAndDisablesWithoutRebuildingCaller(t *testing.T) {
	var calls []string
	p := newDynamicMiddleware(marker("one", &calls))
	h := p.Middleware()(func(context.Context, any) (any, error) { calls = append(calls, "handler"); return "ok", nil })
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	p.Set(marker("two", &calls))
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	p.Set(nil)
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(calls, ","), "one,handler,two,handler,handler"; got != want {
		t.Fatalf("calls=%s want=%s", got, want)
	}
}

func TestMiddlewareUpdateRejectsBBRWithoutChangingOtherPolicies(t *testing.T) {
	logger := newRuntimeTestLogger(t)
	metrics := testMetricsProvider{registry: prometheus.NewRegistry()}
	tracing := runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}
	manager := newMiddlewarePolicyManager(t)
	initial := &config_pb.Server{}
	policies, cleanup, err := newMiddlewarePolicies(manager, logger, initial, metrics, tracing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	next := &config_pb.Server{
		Deadline:  &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(time.Second)},
		RateLimit: &config_pb.Middleware_RateLimit{Enable: proto.Bool(true), BbrLimiter: &config_pb.Middleware_RateLimit_BBRLimiter{Bucket: proto.Int32(0)}},
	}
	defer func() {
		if value := recover(); value != nil {
			t.Fatalf("update panicked instead of rejecting config: %v", value)
		}
	}()
	manager.observer("server", next, nil)
	if !proto.Equal(policies.current, initial) {
		t.Fatalf("invalid update replaced current config: %v", policies.current)
	}
	ctx, cancel, err := policies.deadline.Derive(context.Background(), "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	info, ok := deadline.InfoFromContext(ctx)
	if !ok || info.FallbackTimeout != 10*time.Second || info.RemainingAtApply != 10*time.Second {
		t.Errorf("rejected update changed default deadline: %+v", info)
	}
}

func TestMiddlewarePoliciesApplyValidUpdatesRejectInvalidUpdatesAndCancelOnce(t *testing.T) {
	logger := newRuntimeTestLogger(t)
	metricsProvider := testMetricsProvider{registry: prometheus.NewRegistry()}
	tracingProvider := runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()}
	config, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := newMiddlewarePolicyManager(t)
	policies, cancel, err := newMiddlewarePolicies(
		manager,
		logger,
		config,
		metricsProvider,
		tracingProvider,
	)
	if err != nil {
		t.Fatalf("newMiddlewarePolicies() error = %v", err)
	}

	next := &config_pb.Server{
		Deadline: &config_pb.Middleware_Deadline{
			FallbackTimeout: durationpb.New(time.Second),
		},
		Metadata:  &config_pb.Middleware_Metadata{Prefix: []string{"x-md-"}},
		Tracing:   &config_pb.Middleware_Tracing{Disable: boolp(true)},
		Metrics:   &config_pb.Middleware_Metrics{Disable: boolp(true)},
		Logging:   &config_pb.Middleware_Logging{Disable: boolp(true)},
		Validator: &config_pb.Middleware_Validator{Disable: boolp(true)},
		RateLimit: &config_pb.Middleware_RateLimit{Enable: boolp(true)},
	}
	manager.observer("server", next, nil)
	if !proto.Equal(policies.current, next) {
		t.Fatalf("current middleware = %v, want %v", policies.current, next)
	}
	if got := len(newMiddlewares(policies)); got != 10 {
		t.Fatalf("newMiddlewares() length = %d, want 10", got)
	}

	accepted := proto.CloneOf(policies.current)
	manager.observer(
		"server",
		&config_pb.Server{Metadata: &config_pb.Middleware_Metadata{Prefix: []string{" "}}},
		nil,
	)
	manager.observer("server", "wrong type", nil)
	manager.observer("server", new(config_pb.Server), errors.New("source failed"))
	if !proto.Equal(policies.current, accepted) {
		t.Fatal("invalid middleware update replaced active state")
	}

	cancel()
	cancel()
	if manager.cancelCount != 1 {
		t.Fatalf("cancel count = %d, want 1", manager.cancelCount)
	}
}

func TestMiddlewareSnapshotReplayDoesNotLogUpdate(t *testing.T) {
	logger := newEndpointLog()
	manager := newMiddlewarePolicyManager(t)
	initial := proto.CloneOf(defaultConfig)
	policies, cleanup, err := newMiddlewarePolicies(manager, logger, initial,
		testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	manager.observer("server", proto.CloneOf(initial), nil)
	if len(*logger.entries) != 0 {
		t.Fatal("initial snapshot logged as update")
	}
	next := proto.CloneOf(initial)
	next.Logging = &config_pb.Middleware_Logging{Disable: proto.Bool(!initial.GetLogging().GetDisable())}
	manager.observer("server", next, nil)
	if len(*logger.entries) != 1 || (*logger.entries)[0].level != "info" || (*logger.entries)[0].fields["event"] != "server.middleware.updated" {
		t.Fatalf("changed config logs = %#v", *logger.entries)
	}
	manager.observer("server", proto.CloneOf(next), nil)
	if len(*logger.entries) != 1 {
		t.Fatal("duplicate snapshot logged as update")
	}
	current := policies.current
	restartOnly := proto.CloneOf(next)
	restartOnly.Http.Addr = proto.String("127.0.0.1:18000")
	manager.observer("server", restartOnly, nil)
	if policies.current != current || len(*logger.entries) != 1 {
		t.Fatal("restart-only server change replaced middleware policy")
	}
	manager.observer("server", &config_pb.Server{Metadata: &config_pb.Middleware_Metadata{Prefix: []string{" "}}}, nil)
	if len(*logger.entries) != 2 {
		t.Fatalf("rejected config logs = %#v", *logger.entries)
	}
	rejected := (*logger.entries)[1]
	if rejected.level != "warn" || rejected.fields["event"] != "server.middleware.update.rejected" || rejected.fields["config_key"] != "server" || rejected.fields["error"] == nil {
		t.Fatalf("rejected config log = %#v", rejected)
	}
}

// debugPolicyTransport 只提供此策略实际消费的传输头。
type debugPolicyTransport struct {
	transport.Transporter
	transport.Header
}

func (d debugPolicyTransport) RequestHeader() transport.Header { return d }
func (d debugPolicyTransport) Values(string) []string          { return []string{"1"} }

func TestRequestDebugPolicyHotUpdate(t *testing.T) {
	manager := newMiddlewarePolicyManager(t)
	policies, cleanup, err := newMiddlewarePolicies(manager, newRuntimeTestLogger(t), &config_pb.Server{},
		testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var captured context.Context
	handler := policies.requestDebug.Middleware()(func(ctx context.Context, _ any) (any, error) { captured = ctx; return request.IsDebug(ctx), nil })
	ctx := transport.NewServerContext(context.Background(), debugPolicyTransport{})
	if got, err := handler(ctx, nil); err != nil || got != true {
		t.Fatalf("default accept incoming: got=%v err=%v", got, err)
	}
	var enabledContext context.Context
	for _, enabled := range []bool{false, true, false} {
		manager.observer("server", &config_pb.Server{RequestDebug: &config_pb.Middleware_RequestDebug{AcceptIncoming: proto.Bool(enabled)}}, nil)
		got, err := handler(ctx, nil)
		if err != nil || got != enabled {
			t.Fatalf("enabled=%v got=%v err=%v", enabled, got, err)
		}
		if enabled {
			enabledContext = captured
		}
	}
	if !request.IsDebug(enabledContext) {
		t.Fatal("config update changed an existing request")
	}
	got, err := handler(request.WithDebug(ctx), nil)
	if err != nil || got != true {
		t.Fatalf("local debug lost: %v %v", got, err)
	}
}

func TestMiddlewareTracingOverrideRemovalRestoresStartupDefault(t *testing.T) {
	for _, globalDisabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "global enabled", true: "global disabled"}[globalDisabled], func(t *testing.T) {
			manager := newMiddlewarePolicyManager(t)
			manager.reader = testconfig.NewMany(t, map[string]proto.Message{
				"tracing": &config_pb.Tracing{Disable: proto.Bool(globalDisabled)},
				"server":  &config_pb.Server{Tracing: &config_pb.Middleware_Tracing{Disable: proto.Bool(!globalDisabled)}},
			})
			initial, err := loadConfig(manager)
			if err != nil {
				t.Fatal(err)
			}
			policies, cleanup, err := newMiddlewarePolicies(manager, newRuntimeTestLogger(t), initial,
				testMetricsProvider{registry: prometheus.NewRegistry()}, runtimeTestTracingProvider{provider: tracenoop.NewTracerProvider()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			// 删除局部覆盖时只使用订阅登记的默认模板；运行期不再读取全局配置。
			manager.reader = &serverManagerStub{}
			for _, tracing := range []*config_pb.Middleware_Tracing{nil, {}} {
				next := new(config_pb.Server)
				if err := testconfig.New(t, "server", &config_pb.Server{Tracing: tracing}).Load("server", next, manager.defaults); err != nil {
					t.Fatal(err)
				}
				manager.observer("server", next, nil)
				if got := policies.current.GetTracing().GetDisable(); got != globalDisabled {
					t.Fatalf("removed override tracing.disable = %v, want startup global %v", got, globalDisabled)
				}
			}
		})
	}
}

// middlewarePolicyManager 复用已有订阅替身，并通过真实配置 Reader 读取启动默认值。
type middlewarePolicyManager struct {
	serverManagerStub
	reader   foundationconfig.Reader
	defaults *config_pb.Server
}

func newMiddlewarePolicyManager(t *testing.T) *middlewarePolicyManager {
	t.Helper()
	return &middlewarePolicyManager{reader: testconfig.Empty(t)}
}

func (m *middlewarePolicyManager) Load(key string, target any, defaultValue ...any) error {
	return m.reader.Load(key, target, defaultValue...)
}

func (m *middlewarePolicyManager) Subscribe(key string, prototype any, observer foundationconfig.Observer, defaultValue ...any) (func(), error) {
	m.defaults = proto.CloneOf(defaultValue[0].(*config_pb.Server))
	return m.serverManagerStub.Subscribe(key, prototype, observer, defaultValue...)
}
