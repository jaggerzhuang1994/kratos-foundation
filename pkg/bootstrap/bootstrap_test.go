package bootstrap_test

import (
	"context"
	"io"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
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
			spec.RegisterAppInfo(info)
			spec.RegisterLogger(logger)
			infrastructure := bootstrap.NewInfrastructureBootstrap(bootstrap.AppInfoBootstrap{}, bootstrap.LogBootstrap{}, bootstrap.TracingBootstrap{}, bootstrap.MetricsBootstrap{})
			spec.AddMetadata(map[string]string{"business": "ready"})
			completed := bootstrap.NewApplicationBootstrap(infrastructure, bootstrap.NewRuntimeBootstrap(bootstrap.ServerBootstrap{}, bootstrap.JobBootstrap{}))
			if tt.invalid {
				spec.AddContext(func(context.Context) context.Context { return nil })
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
			assertBootstrapPanic(t, app.ErrSpecFrozen, func() { spec.AddMetadata(nil) })
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
			bootstrap.NewAppInfoBootstrap(spec, appinfo.New("test"))
			return nil
		}},
		{"log", func(spec *app.Spec) error {
			_, cleanup, err := bootstrap.NewLogBootstrap(spec, testconfig.Empty(t), shared)
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			return err
		}},
		{"metrics", func(spec *app.Spec) error {
			bootstrap.NewMetricsBootstrap(spec, noop.NewMeterProvider().Meter("test"))
			return nil
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			spec := app.NewSpec()
			// NewApp 先冻结组装状态，即使后续配置校验失败，也禁止继续贡献。
			if _, err := app.NewApp(context.Background(), spec, nil, nil, nil); err == nil {
				t.Fatal("expected invalid config")
			}
			assertBootstrapPanic(t, app.ErrSpecFrozen, func() {
				if err := tt.register(spec); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func runComponentsApp(t *testing.T, spec *app.Spec) error {
	t.Helper()
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
	ready := bootstrap.NewApplicationBootstrap(bootstrap.InfrastructureBootstrap{}, bootstrap.RuntimeBootstrap{})
	spec.AddContext(func(context.Context) context.Context { return ctx })
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
