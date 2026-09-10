package bootstrap_test

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func TestBootstrapRegistersHTTPAllowsRepeatedRegistration(t *testing.T) {
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t))
	got, err := bootstrap.NewServerBootstrap(spec, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.ServerBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if _, err := bootstrap.NewServerBootstrap(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func TestBootstrapRegistersGRPCAllowsRepeatedRegistration(t *testing.T) {
	runtimeSpec := server.NewSpec()
	runtimeSpec.HTTP().Disable()
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t), runtimeSpec)
	if _, err := bootstrap.NewServerBootstrap(spec, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.NewServerBootstrap(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func TestBootstrapSkipsDisabledProtocols(t *testing.T) {
	runtimeSpec := server.NewSpec()
	runtimeSpec.HTTP().Disable()
	runtimeSpec.GRPC().Disable()
	spec := app.NewSpec()
	runtime := newTestRuntime(t, testconfig.Empty(t), runtimeSpec)
	if _, err := bootstrap.NewServerBootstrap(spec, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.NewServerBootstrap(spec, runtime); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func newTestRuntime(
	t *testing.T,
	manager foundationconfig.Manager,
	optionalSpec ...*server.Spec,
) *server.Runtime {
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
