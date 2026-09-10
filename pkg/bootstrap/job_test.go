package bootstrap_test

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"io"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func TestBootstrapSkipsEmptyManager(t *testing.T) {
	spec := app.NewSpec()
	manager := newTestManager(t, job.NewSpec())
	got, err := bootstrap.NewJobBootstrap(spec, manager)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.JobBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if _, err := bootstrap.NewJobBootstrap(spec, manager); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func TestBootstrapRegistersNonEmptyManagerAllowsRepeatedRegistration(t *testing.T) {
	jobSpec := job.NewSpec()
	jobSpec.RegisterDaemon("worker", job.TaskFunc(func(context.Context) error { return nil }))
	manager := newTestManager(t, jobSpec)
	spec := app.NewSpec()
	if _, err := bootstrap.NewJobBootstrap(spec, manager); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.NewJobBootstrap(spec, manager); err != nil {
		t.Fatalf("second bootstrap error = %v, want nil", err)
	}
}

func newTestManager(t *testing.T, spec *job.Spec) *job.Manager {
	t.Helper()
	logger, tracingProvider, metricsProvider := newTestObservability(t)
	manager, err := job.NewManager(logger, spec, tracingProvider, metricsProvider)
	if err != nil {
		t.Fatal(err)
	}
	return manager
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
			jobSpec := job.NewSpec()
			jobSpec.RegisterOnce("once", job.TaskFunc(func(context.Context) error { return tt.result })).ExitWhenDone()
			manager := newTestManager(t, jobSpec)
			spec := app.NewSpec()
			if _, err := bootstrap.NewJobBootstrap(spec, manager); err != nil {
				t.Fatal(err)
			}
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
			application, err := app.NewApp(ctx, spec, config, policy)
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
