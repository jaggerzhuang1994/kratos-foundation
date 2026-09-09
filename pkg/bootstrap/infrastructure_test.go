package bootstrap_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type bootstrapAppInfo struct {
	id      string
	name    string
	version string
}

func (i bootstrapAppInfo) ID() string                { return i.id }
func (i bootstrapAppInfo) Name() string              { return i.name }
func (i bootstrapAppInfo) Version() string           { return i.version }
func (bootstrapAppInfo) Metadata() map[string]string { return nil }

func TestBootstrapRegistersAppInfoAndAddsServiceLogFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	shared, cleanup, err := foundationlog.NewSharedState(bootstrapLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	info := bootstrapAppInfo{id: "orders-1", name: "orders", version: "v1.2.3"}
	got, err := bootstrap.NewAppInfoBootstrap(info, app.NewSpec(), shared)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.AppInfoBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if err := foundationlog.NewLogger(shared).Log(kratoslog.LevelInfo, "event", "started"); err != nil {
		t.Fatal(err)
	}
	cleanup()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(written)
	for _, field := range []string{"service.id=orders-1", "service.name=orders", "service.version=v1.2.3"} {
		if !strings.Contains(line, field) {
			t.Fatalf("log line lacks %q: %s", field, line)
		}
	}
}

func TestBootstrapStopsBeforeLogOverrideWhenAppInfoRegistrationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	shared, cleanup, err := foundationlog.NewSharedState(bootstrapLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	spec := app.NewSpec()
	first := bootstrapAppInfo{id: "orders-1", name: "orders", version: "v1"}
	if _, err := bootstrap.NewAppInfoBootstrap(first, spec, shared); err != nil {
		t.Fatal(err)
	}
	second := bootstrapAppInfo{id: "billing-1", name: "billing", version: "v2"}
	got, err := bootstrap.NewAppInfoBootstrap(second, spec, shared)
	if err == nil || !strings.Contains(err.Error(), "register app info") ||
		!strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate bootstrap error = %v", err)
	}
	if got != (bootstrap.AppInfoBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if err := foundationlog.NewLogger(shared).Log(kratoslog.LevelInfo, "event", "started"); err != nil {
		t.Fatal(err)
	}
	cleanup()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(written)
	if !strings.Contains(line, "service.name=orders") || strings.Contains(line, "service.name=billing") {
		t.Fatalf("failed registration changed service log fields: %s", line)
	}
}

func bootstrapLogConfig(path string) foundationlog.Config {
	return foundationlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: false,
		TimeFormat:  time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     foundationlog.RotatingConfig{Disable: true},
		},
	}
}

type logBootstrapAppInfo struct{}

func (logBootstrapAppInfo) ID() string                  { return "bootstrap-id" }
func (logBootstrapAppInfo) Name() string                { return "bootstrap-name" }
func (logBootstrapAppInfo) Version() string             { return "bootstrap-version" }
func (logBootstrapAppInfo) Metadata() map[string]string { return nil }

func TestBootstrapRegistersStableLoggerAndRestoresPreviousGlobal(t *testing.T) {
	shared, release, err := foundationlog.NewSharedState(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	got, cleanup, err := bootstrap.NewLogBootstrap(shared, app.NewSpec())
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.LogBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if cleanup == nil {
		t.Fatal("cleanup is nil")
	}
	global := foundationlog.GetLogger()
	if global == previous {
		t.Fatal("global logger was not installed")
	}
	if err := global.Log(kratoslog.LevelInfo, "event", "started"); err != nil {
		t.Fatalf("global logger is not usable: %v", err)
	}

	cleanup()
	if foundationlog.GetLogger() != previous {
		t.Fatal("cleanup did not restore previous global logger")
	}
	cleanup()
	if foundationlog.GetLogger() != previous {
		t.Fatal("cleanup is not idempotent")
	}
}

func TestBootstrapLoggerSurvivesNewAppAndCleanupRestoresPreviousGlobal(t *testing.T) {
	shared, release, err := foundationlog.NewSharedState(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	spec := app.NewSpec()
	_, cleanup, err := bootstrap.NewLogBootstrap(shared, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	installed := foundationlog.GetLogger()
	if installed == previous {
		t.Fatal("log bootstrap did not install its logger")
	}
	if err := spec.RegisterAppInfo(logBootstrapAppInfo{}); err != nil {
		t.Fatal(err)
	}

	manager := testconfig.Empty(t)
	config, err := app.NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	policy, releasePolicy, err := app.NewStopPolicy(config, manager, installed, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releasePolicy)
	if _, err := app.NewApp(context.Background(), spec, config, policy); err != nil {
		t.Fatal(err)
	}
	if foundationlog.GetLogger() != installed {
		t.Fatal("NewApp replaced the logger installed by log bootstrap")
	}

	cleanup()
	if foundationlog.GetLogger() != previous {
		t.Fatal("log bootstrap cleanup did not restore the previous logger after NewApp")
	}
}

func TestBootstrapRegistrationFailureLeavesGlobalLoggerUntouched(t *testing.T) {
	shared, release, err := foundationlog.NewSharedState(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	spec := app.NewSpec()
	if err := spec.RegisterLogger(kratoslog.NewStdLogger(io.Discard)); err != nil {
		t.Fatal(err)
	}
	got, cleanup, err := bootstrap.NewLogBootstrap(shared, spec)
	if err == nil || !strings.Contains(err.Error(), "register app logger") ||
		!strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate bootstrap error = %v", err)
	}
	if got != (bootstrap.LogBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if cleanup != nil {
		t.Fatal("failed bootstrap returned cleanup")
	}
	if foundationlog.GetLogger() != previous {
		t.Fatal("failed bootstrap changed global logger")
	}
}

func bootstrapConfig() foundationlog.Config {
	return foundationlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{
				Disable: true,
				Level:   kratoslog.LevelInfo,
			},
		},
	}
}

func TestBootstrapAddsTraceAndSpanFieldsWithoutServiceName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	config := foundationlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: false,
		TimeFormat:  time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     foundationlog.RotatingConfig{Disable: true},
		},
	}
	shared, cleanup, err := foundationlog.NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	got, err := bootstrap.NewTracingBootstrap(shared)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.TracingBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	logger := foundationlog.NewLogger(shared).WithContext(ctx)
	if err := logger.Log(kratoslog.LevelInfo, "event", "traced"); err != nil {
		t.Fatal(err)
	}
	cleanup()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(written)
	for _, field := range []string{
		"trace.id=0102030405060708090a0b0c0d0e0f10",
		"span.id=1112131415161718",
	} {
		if !strings.Contains(line, field) {
			t.Fatalf("log line lacks %q: %s", field, line)
		}
	}
	if strings.Contains(line, foundationlog.ServiceNameKey+"=") {
		t.Fatalf("tracing bootstrap added service.name: %s", line)
	}
}

func TestBootstrapAllowsRepeatedMetricsContextContributions(t *testing.T) {
	spec := app.NewSpec()
	meter := noop.NewMeterProvider().Meter("test")
	got, err := bootstrap.NewMetricsBootstrap(spec, meter)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.MetricsBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	got, err = bootstrap.NewMetricsBootstrap(spec, meter)
	if err != nil {
		t.Fatalf("repeated bootstrap error = %v", err)
	}
	if got != (bootstrap.MetricsBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
}
