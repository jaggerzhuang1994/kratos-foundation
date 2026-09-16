package bootstrap_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func serverTestDependencies(t *testing.T, manager foundationconfig.Manager) (log.Logger, metrics.Provider, tracing.Provider) {
	t.Helper()
	info := appinfo.New("test")
	shared, cleanupLog, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	metricsProvider, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	tracingProvider, cleanupTracing, err := tracing.NewProvider(manager, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)

	return shared, metricsProvider, tracingProvider
}

func TestServerBootstrapPropagatesRegistrationFailure(t *testing.T) {
	failure := errors.New("endpoint rejected")
	for _, protocol := range []string{"http", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			spec := newTestSpec()
			if protocol == "http" {
				spec.Http().Register(func(server.HTTPServer) error { return failure })
			} else {
				spec.Grpc().Register(func(server.GRPCServer) error { return failure })
			}
			logger, metrics, tracing := serverTestDependencies(t, testconfig.Empty(t))
			_, cleanup, err := bootstrap.NewServerBootstrap(spec.application, spec.servers, testconfig.Empty(t), logger, metrics, tracing, bootstrap.Bootstrap{})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if cleanup != nil {
				t.Fatal("failure returned owned resources")
			}
		})
	}
}

// 配置声明的监听也必须进入 App 登记，不能因未调用 Http/Grpc 而跳过。
func TestServerBootstrapConfigOnlyListeners(t *testing.T) {
	for _, tt := range []struct {
		name        string
		config      *config_pb.Server
		wantRuntime bool
	}{
		{"http default", &config_pb.Server{}, true},
		{"grpc explicit", &config_pb.Server{Http: &config_pb.HttpServerOption{Disable: proto.Bool(true)}, Grpc: &config_pb.GrpcServerOption{Disable: proto.Bool(false)}}, true},
		{"management only", &config_pb.Server{Http: &config_pb.HttpServerOption{Disable: proto.Bool(true), Health: &config_pb.HttpServerOption_Health{Addr: proto.String("127.0.0.1:0")}}}, true},
		{"all disabled", &config_pb.Server{Http: &config_pb.HttpServerOption{Disable: proto.Bool(true)}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := newTestSpec()
			if tt.name == "management only" {
				spec.Health().Checks(server.ReadinessCheck{Name: "database", Check: func(context.Context) error { return nil }})
			}
			// 冻结 App 后，任何实际登记的监听都必须 panic(ErrSpecFrozen)。
			_, _ = app.NewApp(context.Background(), spec.application, nil, nil, nil)
			manager := testconfig.New(t, "server", tt.config)
			logger, meter, tracer := serverTestDependencies(t, manager)
			tracked := &serverSubscriptionTracker{Manager: manager}
			assemble := func() {
				_, cleanup, err := bootstrap.NewServerBootstrap(spec.application, spec.servers, tracked, logger, meter, tracer, bootstrap.Bootstrap{})
				if cleanup != nil {
					cleanup()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if tt.wantRuntime {
				assertBootstrapPanic(t, app.ErrSpecFrozen, assemble)
			} else {
				assemble()
			}
			if tracked.subscribed == 0 || tracked.active != 0 {
				t.Fatalf("subscriptions=%d active=%d", tracked.subscribed, tracked.active)
			}

		})
	}
}

// 仅跟踪当前 provider 的订阅资源，检查登记 panic 不泄漏构造期资源。
type serverSubscriptionTracker struct {
	foundationconfig.Manager
	subscribed, active int
}

func (m *serverSubscriptionTracker) Subscribe(key string, target any, observer foundationconfig.Observer, defaults ...any) (func(), error) {
	cancel, err := m.Manager.Subscribe(key, target, observer, defaults...)
	if err != nil {
		return nil, err
	}
	m.subscribed++
	m.active++
	return func() { cancel(); m.active-- }, nil
}

func TestJobBootstrapSelection(t *testing.T) {
	for _, selection := range []string{"empty", "daemon", "invalid", "frozen"} {
		t.Run(selection, func(t *testing.T) {
			spec := newTestSpec()
			switch selection {
			case "empty":
				spec.Job()
			case "daemon", "frozen":
				spec.Job().RegisterDaemon("worker", job.TaskFunc(func(context.Context) error { return nil }))
			case "invalid":
				spec.Job().RegisterCron("invalid", "not a schedule", job.TaskFunc(func(context.Context) error { return nil }))
			}
			serverBootstrap := prepareServer(t, spec)
			if selection == "frozen" {
				_, _ = app.NewApp(context.Background(), spec.application, nil, nil, nil)
			}
			logger, tracer, meter := newTestObservability(t)
			if selection == "frozen" {
				assertBootstrapPanic(t, app.ErrSpecFrozen, func() {
					_, _ = bootstrap.NewJobBootstrap(spec.application, spec.jobs, nil, logger, meter, tracer, serverBootstrap)
				})
				return
			}
			_, err := bootstrap.NewJobBootstrap(spec.application, spec.jobs, nil, logger, meter, tracer, serverBootstrap)
			if (err != nil) != (selection == "invalid") {
				t.Fatal(err)
			}

		})
	}

}

func newTestObservability(t *testing.T) (log.Logger, tracing.Provider, metrics.Provider) {
	t.Helper()
	info := appinfo.New("test")
	shared, cleanupLog, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupLog)
	metricsProvider, cleanupMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupMetrics)
	configManager := testconfig.Empty(t)
	tracingProvider, cleanupTracing, err := tracing.NewProvider(configManager, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupTracing)
	return shared, tracingProvider, metricsProvider
}

// 通过真实 App 生命周期验证组装适配：成功完成退出，失败仍可由 errors.Is 找到。
func TestJobBootstrapPreservesCompletionAndFailure(t *testing.T) {
	failure := errors.New("once task failed")
	for _, tt := range []struct {
		name   string
		result error
	}{{"completed", nil}, {"failed", failure}} {
		t.Run(tt.name, func(t *testing.T) {
			components := newTestSpec()
			components.Job().RegisterOnce("once", job.TaskFunc(func(context.Context) error { return tt.result })).ExitWhenDone()
			jobLogger, tracer, meter := newTestObservability(t)
			spec := components.application
			serverBootstrap := prepareServer(t, components)
			if _, err := bootstrap.NewJobBootstrap(components.application, components.jobs, nil, jobLogger, meter, tracer, serverBootstrap); err != nil {
				t.Fatal(err)
			}
			spec.RegisterAppInfo(appinfo.New("test"))
			logger := kratoslog.NewStdLogger(io.Discard)
			spec.RegisterLogger(logger)
			configs := testconfig.Empty(t)
			config, err := app.NewConfig(configs)
			if err != nil {
				t.Fatal(err)
			}
			policy, release, err := app.NewStopPolicy(config, configs, logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			application, err := app.NewApp(ctx, spec, config, policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = application.Run()
			if tt.result == nil && err != nil {
				t.Fatalf("completed job: %v", err)
			}
			if tt.result != nil && !errors.Is(err, tt.result) {
				t.Fatalf("job failure = %v, want %v", err, tt.result)
			}
			if ctx.Err() != nil {
				t.Fatalf("job did not stop App before deadline: %v", ctx.Err())
			}
		})
	}
}

// 嵌入接口仅用于构造契约：Bootstrap 不应在构造期执行任务或调用协调器。
type constructionCoordinator struct{ job.ConcurrencyCoordinator }

func TestJobBootstrapInjectsCoordinator(t *testing.T) {
	logger, tracing, metrics := newTestObservability(t)
	spec := newTestSpec()
	spec.Job().RegisterCron("distributed", "@hourly", job.TaskFunc(func(context.Context) error { return nil }), job.WithConcurrentPolicy(job.SkipIfDistributedRunning))
	serverBootstrap := prepareServer(t, spec)
	_, err := bootstrap.NewJobBootstrap(spec.application, spec.jobs, constructionCoordinator{}, logger, metrics, tracing, serverBootstrap)
	if err != nil {
		t.Fatal(err)
	}
}

// 任务测试按 Wire 顺序构造服务器；关闭业务监听，避免生命周期测试占用固定端口。
func prepareServer(t *testing.T, spec *testSpec) bootstrap.ServerBootstrap {
	t.Helper()
	logger, tracer, meter := newTestObservability(t)
	manager := testconfig.New(t, "server", &config_pb.Server{Http: &config_pb.HttpServerOption{Disable: proto.Bool(true)}})
	marker, cleanup, err := bootstrap.NewServerBootstrap(spec.application, spec.servers, manager, logger, meter, tracer, bootstrap.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return marker
}

type deliveryConsumer struct{}

// taskStore 让应用完成一次真实 Worker 调用后用存储故障结束，验证登记及退出传播。
type taskStore struct {
	queue.Store
	acknowledged bool
	failure      error
}

func (s *taskStore) Reserve(context.Context, time.Time, time.Duration) (*queue.Reservation, error) {
	if s.acknowledged {
		return nil, s.failure
	}
	return &queue.Reservation{Task: &queue.Task{ID: "one", Type: "test"}, Token: "lease", Attempts: 1}, nil
}

func (s *taskStore) Ack(context.Context, *queue.Reservation) error { s.acknowledged = true; return nil }

func (deliveryConsumer) Consume(ctx context.Context, handler kafka.DeliveryHandler) error {
	return handler(ctx, kafka.Delivery{Message: &kafka.Message{ID: "one"}})
}

func TestRuntimeBootstrapWorkerLifecycle(t *testing.T) {
	failure := errors.New("worker failed")
	for _, mode := range []string{"once", "consumer", "queue"} {
		t.Run(mode, func(t *testing.T) {
			components := newTestSpec()
			logger, tracing, metrics := newTestObservability(t)
			called := false
			var register func() *bootstrap.Spec
			switch mode {
			case "once":
				components.Job().RegisterOnce("once", job.TaskFunc(func(context.Context) error { called = true; return nil })).ExitWhenDone()
			case "consumer":
				runtime, err := kafka.NewConsumerRuntime(kafka.RuntimeConfig{Name: "consumer", Destination: "events"}, deliveryConsumer{}, func(context.Context, *kafka.Message) error {
					called = true
					return failure
				}, kafka.Observability{Logger: logger, Tracing: tracing, Metrics: metrics})
				if err != nil {
					t.Fatal(err)
				}
				register = func() *bootstrap.Spec { return components.RegisterKafkaConsumer(runtime) }
			case "queue":
				worker, err := queue.NewWorker(queue.WorkerConfig{Name: "tasks", Queue: "test"}, &taskStore{failure: failure}, map[string]queue.Handler{"test": func(context.Context, *queue.Task) error { called = true; return nil }}, queue.Observability{Logger: logger, Tracing: tracing, Metrics: metrics})
				if err != nil {
					t.Fatal(err)
				}
				register = func() *bootstrap.Spec { return components.RegisterQueueWorker(worker) }
			}
			if register != nil && register() != components.Spec {
				t.Fatal("typed registration did not return the same Spec")
			}
			spec := components.application
			serverBootstrap := prepareServer(t, components)
			_, err := bootstrap.NewJobBootstrap(components.application, components.jobs, nil, logger, metrics, tracing, serverBootstrap)
			if err != nil {
				t.Fatal(err)
			}
			err = runComponentsApp(t, spec)
			if mode == "once" && err != nil {
				t.Fatal(err)
			}
			if mode != "once" && !errors.Is(err, failure) {
				t.Fatalf("consumer error = %v", err)
			}
			if !called {
				t.Fatal("selected component was not started")
			}
		})
	}
}
