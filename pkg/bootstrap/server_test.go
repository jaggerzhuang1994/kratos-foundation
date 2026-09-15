package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func TestBootstrapRegistersHTTPAllowsRepeatedRegistration(t *testing.T) {
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t))
	err := registerServer(spec, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerServer(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func TestBootstrapRegistersGRPCAllowsRepeatedRegistration(t *testing.T) {
	runtimeSpec := server.NewSpec()
	runtimeSpec.HTTP().Disable()
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t), runtimeSpec)
	if err := registerServer(spec, runtime); err != nil {
		t.Fatal(err)
	}
	if err := registerServer(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func TestBootstrapSkipsDisabledProtocols(t *testing.T) {
	runtimeSpec := server.NewSpec()
	runtimeSpec.HTTP().Disable()
	runtimeSpec.GRPC().Disable()
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t), runtimeSpec)
	if err := registerServer(spec, runtime); err != nil {
		t.Fatal(err)
	}
	if err := registerServer(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func newTestRuntime(
	t *testing.T,
	manager foundationconfig.Manager,
	optionalSpec ...*server.Spec,
) *server.Runtime {
	t.Helper()
	shared, metricsProvider, tracingProvider := serverTestDependencies(t, manager)
	spec := server.NewSpec()
	if len(optionalSpec) != 0 {
		spec = optionalSpec[0]
	}
	runtime, cleanupRuntime, err := server.NewRuntime(
		manager,
		shared,
		metricsProvider,
		tracingProvider,
		spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupRuntime)
	return runtime
}

func TestRegisterServerRejectsFrozenSpec(t *testing.T) {
	for _, protocol := range []string{"http", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			runtimeSpec := server.NewSpec()
			if protocol == "grpc" {
				runtimeSpec.HTTP().Disable()
			}
			runtime := newTestRuntime(t, testconfig.Empty(t), runtimeSpec)
			spec := app.NewSpec()
			if _, err := app.NewApp(context.Background(), spec, nil, nil, nil); err == nil {
				t.Fatal("invalid app accepted")
			}
			if err := registerServer(spec, runtime); !errors.Is(err, app.ErrSpecFrozen) {
				t.Fatal(err)
			}
		})
	}
}

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
			spec := NewSpec()
			if protocol == "http" {
				spec.Http().Register(func(server.HTTPServer) error { return failure })
			} else {
				spec.Grpc().Register(func(server.GRPCServer) error { return failure })
			}
			logger, metrics, tracing := serverTestDependencies(t, testconfig.Empty(t))
			_, cleanup, err := NewServerBootstrap(spec, testconfig.Empty(t), logger, metrics, tracing, Bootstrap{})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if cleanup != nil {
				t.Fatal("failure returned owned resources")
			}
		})
	}
}

func TestNewServerBootstrapSelectionAndRollback(t *testing.T) {
	empty := NewSpec()
	_, cleanup, err := NewServerBootstrap(empty, nil, nil, nil, nil, Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, _, err := NewServerBootstrap(empty, nil, nil, nil, nil, Bootstrap{}); err == nil {
		t.Fatal("repeated server assembly accepted")
	}
	if _, _, err := NewServerBootstrap(nil, nil, nil, nil, nil, Bootstrap{}); err == nil {
		t.Fatal("nil spec accepted")
	}
	for _, frozen := range []bool{false, true} {
		t.Run(fmt.Sprint(frozen), func(t *testing.T) {
			spec := NewSpec()
			spec.Http()
			if frozen {
				_, _ = app.NewApp(context.Background(), ApplicationSpec(spec), nil, nil, nil)
			}
			manager := testconfig.Empty(t)
			logger, meter, tracer := serverTestDependencies(t, manager)
			_, release, err := NewServerBootstrap(spec, manager, logger, meter, tracer, Bootstrap{})
			if frozen {
				if !errors.Is(err, app.ErrSpecFrozen) || release != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				release()
			}
		})
	}
}
