package bootstrap_test

import (
	"context"
	"errors"
	"io"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewKratosAppConsumesContributionsAndFreezesSpec(t *testing.T) {
	manager := testconfig.Empty(t)
	config, err := app.NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	logger := kratoslog.NewStdLogger(io.Discard)
	policy, cleanup, err := app.NewStopPolicy(config, manager, logger, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	info := appinfo.New("bootstrap-test")
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		wantErr bool
	}{
		{"success", context.Background(), false},
		{"invalid context", nil, true},
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
			completed := bootstrap.NewBootstrap(infrastructure, bootstrap.UserBootstrap{})
			application, err := bootstrap.NewKratosApp(tt.ctx, spec, completed, config, policy)
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
	shared, release, err := log.NewSharedState(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	jobSpec := job.NewSpec()
	jobSpec.Once("once", job.TaskFunc(func(context.Context) error { return nil }))
	manager := newTestManager(t, jobSpec)
	httpRuntime := newTestRuntime(t, testconfig.Empty(t))
	grpcSpec := server.NewSpec()
	grpcSpec.HTTP().Disable()
	grpcRuntime := newTestRuntime(t, testconfig.Empty(t), grpcSpec)
	cases := []struct {
		name     string
		register func(*app.Spec) error
	}{
		{"appinfo", func(spec *app.Spec) error {
			_, err := bootstrap.NewAppInfoBootstrap(appinfo.New("test"), spec, shared)
			return err
		}},
		{"log", func(spec *app.Spec) error {
			_, cleanup, err := bootstrap.NewLogBootstrap(shared, spec)
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			return err
		}},
		{"metrics", func(spec *app.Spec) error {
			_, err := bootstrap.NewMetricsBootstrap(spec, noop.NewMeterProvider().Meter("test"))
			return err
		}},
		{"http", func(spec *app.Spec) error { _, err := bootstrap.NewServerBootstrap(spec, httpRuntime); return err }},
		{"grpc", func(spec *app.Spec) error { _, err := bootstrap.NewServerBootstrap(spec, grpcRuntime); return err }},
		{"job", func(spec *app.Spec) error { _, err := bootstrap.NewJobBootstrap(spec, manager); return err }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			spec := app.NewSpec()
			// NewApp 先冻结组装状态，即使后续配置校验失败，也禁止继续贡献。
			if _, err := app.NewApp(context.Background(), spec, nil, nil); err == nil {
				t.Fatal("expected invalid config")
			}
			if err := tt.register(spec); !errors.Is(err, app.ErrSpecFrozen) {
				t.Fatalf("register = %v, want ErrSpecFrozen", err)
			}
		})
	}
}
