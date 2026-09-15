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

func TestJobBootstrapSelection(t *testing.T) {
	for _, selection := range []string{"none", "empty", "daemon", "invalid", "frozen"} {
		t.Run(selection, func(t *testing.T) {
			spec := bootstrap.NewSpec()
			switch selection {
			case "empty":
				spec.Job()
			case "daemon", "frozen":
				spec.Job().RegisterDaemon("worker", job.TaskFunc(func(context.Context) error { return nil }))
			case "invalid":
				spec.Job().RegisterCron("invalid", "not a schedule", job.TaskFunc(func(context.Context) error { return nil }))
			}
			if selection == "frozen" {
				_, _ = app.NewApp(context.Background(), bootstrap.ApplicationSpec(spec), nil, nil, nil)
			}
			logger, tracer, meter := newTestObservability(t)
			_, err := bootstrap.NewJobBootstrap(spec, nil, logger, meter, tracer, bootstrap.Bootstrap{})
			if (err != nil) != (selection == "invalid" || selection == "frozen") {
				t.Fatal(err)
			}
			if selection == "frozen" && !errors.Is(err, app.ErrSpecFrozen) {
				t.Fatal(err)
			}
			if _, err := bootstrap.NewJobBootstrap(spec, nil, logger, meter, tracer, bootstrap.Bootstrap{}); err == nil {
				t.Fatal("repeated assembly accepted")
			}
		})
	}
	if _, err := bootstrap.NewJobBootstrap(nil, nil, nil, nil, nil, bootstrap.Bootstrap{}); err == nil {
		t.Fatal("nil spec accepted")
	}
	if _, err := bootstrap.NewJobBootstrap(bootstrap.NewSpec(), nil, nil, nil, nil, bootstrap.Bootstrap{}); err != nil {
		t.Fatal(err)
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
			components := bootstrap.NewSpec()
			components.Job().RegisterOnce("once", job.TaskFunc(func(context.Context) error { return tt.result })).ExitWhenDone()
			jobLogger, tracer, meter := newTestObservability(t)
			spec := bootstrap.ApplicationSpec(components)
			if _, err := bootstrap.NewJobBootstrap(components, nil, jobLogger, meter, tracer, bootstrap.Bootstrap{}); err != nil {
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
	spec := bootstrap.NewSpec()
	spec.Job().RegisterCron("distributed", "@hourly", job.TaskFunc(func(context.Context) error { return nil }), job.WithConcurrentPolicy(job.SkipIfDistributedRunning))
	_, err := bootstrap.NewJobBootstrap(spec, constructionCoordinator{}, logger, metrics, tracing, bootstrap.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
}
