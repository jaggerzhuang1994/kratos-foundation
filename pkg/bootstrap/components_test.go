package bootstrap_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

func TestComponentsEmptyDoesNotRequireInfrastructure(t *testing.T) {
	spec := bootstrap.NewSpec()
	got, cleanup, err := bootstrap.NewComponentsBootstrap(spec, nil, nil, nil, nil, bootstrap.Bootstrap{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if got.StopDelay() != 0 {
		t.Fatal("empty application has server stop delay")
	}
	if _, _, err := bootstrap.NewComponentsBootstrap(spec, nil, nil, nil, nil, bootstrap.Bootstrap{}, nil); err == nil {
		t.Fatal("reused Spec was accepted")
	}
}

func TestComponentsPropagateRegistrationFailure(t *testing.T) {
	failure := errors.New("endpoint rejected")
	for _, protocol := range []string{"http", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			spec := bootstrap.NewSpec()
			if protocol == "http" {
				spec.Http().Register(func(server.HTTPServer) error { return failure })
			} else {
				spec.Grpc().Register(func(server.GRPCServer) error { return failure })
			}
			logger, tracing, metrics := newTestObservability(t)
			_, cleanup, err := bootstrap.NewComponentsBootstrap(spec, testconfig.Empty(t), logger, metrics, tracing, bootstrap.Bootstrap{}, nil)
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if cleanup != nil {
				t.Fatal("failure returned owned resources")
			}
		})
	}
}

func TestComponentsRejectInvalidSelections(t *testing.T) {
	for _, name := range []string{"job", "runtime"} {
		t.Run(name, func(t *testing.T) {
			spec := bootstrap.NewSpec()
			switch name {
			case "job":
				spec.Job().RegisterCron("invalid", "not a schedule", job.TaskFunc(func(context.Context) error { return nil }))
			case "runtime":
				spec.RegisterRuntime(nil)
			}
			logger, tracing, metrics := newTestObservability(t)
			if _, _, err := bootstrap.NewComponentsBootstrap(spec, nil, logger, metrics, tracing, bootstrap.Bootstrap{}, nil); err == nil {
				t.Fatal("invalid selection was accepted")
			}
		})
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

func TestComponentsWorkerLifecycle(t *testing.T) {
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
			// nil config manager 证明 worker 没有尝试构造 HTTP/gRPC Runtime。
			_, cleanup, err := bootstrap.NewComponentsBootstrap(components, nil, logger, metrics, tracing, bootstrap.Bootstrap{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
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
	policy, release, err := app.NewStopPolicy(config, configs, logger, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := bootstrap.NewApplicationBootstrap(bootstrap.InfrastructureBootstrap{}, bootstrap.ComponentsBootstrap{})
	if err := spec.AddContext(func(context.Context) context.Context { return ctx }); err != nil {
		t.Fatal(err)
	}
	application, err := bootstrap.NewKratosApp(spec, ready, config, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = application.Run()
	if ctx.Err() != nil {
		t.Fatal("application did not stop before deadline")
	}
	return err
}

// 嵌入接口仅用于构造契约：Bootstrap 不应在构造期执行任务或调用协调器。
type constructionCoordinator struct{ job.ConcurrencyCoordinator }

func TestComponentsInjectJobCoordinator(t *testing.T) {
	logger, tracing, metrics := newTestObservability(t)
	spec := bootstrap.NewSpec()
	spec.Job().RegisterCron("distributed", "@hourly", job.TaskFunc(func(context.Context) error { return nil }), job.WithConcurrentPolicy(job.SkipIfDistributedRunning))
	_, cleanup, err := bootstrap.NewComponentsBootstrap(spec, nil, logger, metrics, tracing, bootstrap.Bootstrap{}, constructionCoordinator{})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
}
