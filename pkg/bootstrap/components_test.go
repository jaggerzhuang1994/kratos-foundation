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
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

func TestComponentsEmptyDoesNotRequireInfrastructure(t *testing.T) {
	spec := bootstrap.NewSpec()
	got, cleanup, err := bootstrap.NewComponentsBootstrap(spec, nil, nil, nil, nil, bootstrap.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if got.StopDelay() != 0 {
		t.Fatal("empty application has server stop delay")
	}
	if _, _, err := bootstrap.NewComponentsBootstrap(spec, nil, nil, nil, nil, bootstrap.Bootstrap{}); err == nil {
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
			_, cleanup, err := bootstrap.NewComponentsBootstrap(spec, testconfig.Empty(t), logger, metrics, tracing, bootstrap.Bootstrap{})
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
			if _, _, err := bootstrap.NewComponentsBootstrap(spec, nil, logger, metrics, tracing, bootstrap.Bootstrap{}); err == nil {
				t.Fatal("invalid selection was accepted")
			}
		})
	}
}

type deliveryConsumer struct{}

func (deliveryConsumer) Consume(ctx context.Context, handler queue.DeliveryHandler) error {
	return handler(ctx, queue.Delivery{Message: &queue.Message{ID: "one"}})
}

func TestComponentsWorkerLifecycle(t *testing.T) {
	failure := errors.New("worker failed")
	for _, mode := range []string{"once", "consumer"} {
		t.Run(mode, func(t *testing.T) {
			components := bootstrap.NewSpec()
			logger, tracing, metrics := newTestObservability(t)
			called := false
			if mode == "once" {
				components.Job().RegisterOnce("once", job.TaskFunc(func(context.Context) error { called = true; return nil })).ExitWhenDone()
			} else {
				runtime, err := queue.NewConsumerRuntime(queue.RuntimeConfig{Name: "consumer", Destination: "events"}, deliveryConsumer{}, func(context.Context, *queue.Message) error {
					called = true
					return failure
				}, queue.Observability{Logger: logger, Tracing: tracing, Metrics: metrics})
				if err != nil {
					t.Fatal(err)
				}
				components.RegisterRuntime(runtime)
			}
			spec := bootstrap.ApplicationSpec(components)
			// nil config manager 证明 worker 没有尝试构造 HTTP/gRPC Runtime。
			_, cleanup, err := bootstrap.NewComponentsBootstrap(components, nil, logger, metrics, tracing, bootstrap.Bootstrap{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			err = runComponentsApp(t, spec)
			if mode == "once" && err != nil {
				t.Fatal(err)
			}
			if mode == "consumer" && !errors.Is(err, failure) {
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
	application, err := bootstrap.NewKratosApp(ctx, spec, ready, config, policy)
	if err != nil {
		t.Fatal(err)
	}
	err = application.Run()
	if ctx.Err() != nil {
		t.Fatal("application did not stop before deadline")
	}
	return err
}
