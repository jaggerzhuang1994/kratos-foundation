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
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewKratosAppConsumesContributionsAndFreezesSpec(t *testing.T) {
	manager := testconfig.Empty(t)
	config, err := app.NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	logger := kratoslog.NewStdLogger(io.Discard)
	policy, cleanup, err := app.NewStopPolicy(config, manager, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	info := appinfo.New("bootstrap-test")
	for _, tt := range []struct {
		name    string
		invalid bool
		wantErr bool
	}{
		{"success", false, false},
		{"invalid context decorator", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := app.NewSpec()
			if err := spec.RegisterAppInfo(info); err != nil {
				t.Fatal(err)
			}
			if err := spec.RegisterLogger(logger); err != nil {
				t.Fatal(err)
			}
			infrastructure := bootstrap.NewInfrastructureBootstrap(bootstrap.AppInfoBootstrap{}, bootstrap.LogBootstrap{}, bootstrap.TracingBootstrap{}, bootstrap.MetricsBootstrap{})
			if err := spec.AddMetadata(map[string]string{"business": "ready"}); err != nil {
				t.Fatal(err)
			}
			completed := bootstrap.NewApplicationBootstrap(infrastructure, bootstrap.RuntimeBootstrap{})
			if tt.invalid {
				if err := spec.AddContext(func(context.Context) context.Context { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			application, err := bootstrap.NewKratosApp(spec, config, policy, nil, completed)
			if tt.wantErr {
				if err == nil || application != nil {
					t.Fatalf("construction = (%v, %v), want error", application, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if application.Version() != "bootstrap-test" || application.Metadata()["business"] != "ready" {
				t.Fatal("application contributions missing")
			}
			if err := spec.AddMetadata(nil); !errors.Is(err, app.ErrSpecFrozen) {
				t.Fatalf("late contribution = %v", err)
			}
		})
	}
}

func TestContributionsRejectFrozenSpec(t *testing.T) {
	shared, release, err := testlog.New(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	cases := []struct {
		name     string
		register func(*app.Spec) error
	}{
		{"appinfo", func(spec *app.Spec) error {
			_, err := bootstrap.NewAppInfoBootstrap(spec, appinfo.New("test"))
			return err
		}},
		{"log", func(spec *app.Spec) error {
			_, cleanup, err := bootstrap.NewLogBootstrap(spec, testconfig.Empty(t), shared)
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			return err
		}},
		{"metrics", func(spec *app.Spec) error {
			_, err := bootstrap.NewMetricsBootstrap(spec, noop.NewMeterProvider().Meter("test"))
			return err
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			spec := app.NewSpec()
			// NewApp 先冻结组装状态，即使后续配置校验失败，也禁止继续贡献。
			if _, err := app.NewApp(context.Background(), spec, nil, nil, nil); err == nil {
				t.Fatal("expected invalid config")
			}
			if err := tt.register(spec); !errors.Is(err, app.ErrSpecFrozen) {
				t.Fatalf("register = %v, want ErrSpecFrozen", err)
			}
		})
	}
}

func TestRuntimeBootstrapEmptyDoesNotRequireInfrastructure(t *testing.T) {
	spec := bootstrap.NewSpec()
	_, err := bootstrap.NewRuntimeBootstrap(spec, bootstrap.ServerBootstrap{}, bootstrap.JobBootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.NewRuntimeBootstrap(spec, bootstrap.ServerBootstrap{}, bootstrap.JobBootstrap{}); err == nil {
		t.Fatal("reused Spec was accepted")
	}
}

func TestRuntimeBootstrapRejectInvalidRuntime(t *testing.T) {
	spec := bootstrap.NewSpec()
	spec.RegisterRuntime(nil)
	if _, err := bootstrap.NewRuntimeBootstrap(spec, bootstrap.ServerBootstrap{}, bootstrap.JobBootstrap{}); err == nil {
		t.Fatal("nil runtime accepted")
	}
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
			components := bootstrap.NewSpec()
			logger, tracing, metrics := newTestObservability(t)
			called := false
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
				components.RegisterRuntime(runtime)
			case "queue":
				worker, err := queue.NewWorker(queue.WorkerConfig{Name: "tasks", Queue: "test"}, &taskStore{failure: failure}, map[string]queue.Handler{"test": func(context.Context, *queue.Task) error { called = true; return nil }}, queue.Observability{Logger: logger, Tracing: tracing, Metrics: metrics})
				if err != nil {
					t.Fatal(err)
				}
				components.RegisterRuntime(worker)
			}
			spec := bootstrap.ApplicationSpec(components)
			jobBootstrap, err := bootstrap.NewJobBootstrap(components, nil, logger, metrics, tracing, bootstrap.Bootstrap{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = bootstrap.NewRuntimeBootstrap(components, bootstrap.ServerBootstrap{}, jobBootstrap)
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

func runComponentsApp(t *testing.T, spec *app.Spec) error {
	t.Helper()
	if err := spec.RegisterAppInfo(appinfo.New("test")); err != nil {
		t.Fatal(err)
	}
	logger := kratoslog.NewStdLogger(io.Discard)
	if err := spec.RegisterLogger(logger); err != nil {
		t.Fatal(err)
	}
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
	ready := bootstrap.NewApplicationBootstrap(bootstrap.InfrastructureBootstrap{}, bootstrap.RuntimeBootstrap{})
	if err := spec.AddContext(func(context.Context) context.Context { return ctx }); err != nil {
		t.Fatal(err)
	}
	application, err := bootstrap.NewKratosApp(spec, config, policy, nil, ready)
	if err != nil {
		t.Fatal(err)
	}
	err = application.Run()
	if ctx.Err() != nil {
		t.Fatal("application did not stop before deadline")
	}
	return err
}
