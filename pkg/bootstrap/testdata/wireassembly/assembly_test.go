package wireassembly

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestGeneratedAssemblyAndCleanup(t *testing.T) {
	previous := kratoslog.GetLogger()
	built, cleanup, err := initialize(businessSources(t), "wire-test", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if built.App.Metadata()["business"] != "ready" {
		t.Fatal("business contribution missing before Spec freeze")
	}
	if built.App.ID() != built.Info.ID() || built.App.Name() != built.Info.Name() || built.App.Version() != "wire-test" {
		t.Fatal("appinfo Bootstrap did not contribute application identity")
	}
	if err := built.Spec.AddMetadata(nil); !errors.Is(err, app.ErrSpecFrozen) {
		t.Fatalf("Spec must be frozen after all Bootstraps: %v", err)
	}
	if kratoslog.GetLogger() == previous {
		t.Fatal("log Bootstrap did not install application logger")
	}
	if err := built.Logger.Log(kratoslog.LevelInfo, "msg", "assembled"); err != nil {
		t.Fatal(err)
	}
	if built.Metrics.MeterProvider() == nil || !built.Tracing.Disabled() || built.Tracer == nil {
		t.Fatal("metrics and disabled tracing providers were not assembled")
	}

	cleanup()
	cleanup()
	if kratoslog.GetLogger() != previous {
		t.Fatal("generated cleanup did not restore previous global logger")
	}
	if err := built.Logger.Log(kratoslog.LevelInfo, "msg", "after cleanup"); err == nil {
		t.Fatal("generated cleanup did not release SharedState")
	}
	if err := built.Manager.Load("app", new(config_pb.App)); !errors.Is(err, config.ErrManagerClosed) {
		t.Fatalf("generated cleanup did not close config Manager: %v", err)
	}
}

func TestComponentsDeclaredInBootRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	application, cleanup, err := initializeComponents(businessSources(t), "boot-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if application.Metadata()["boot"] != "declared" {
		t.Fatal("Boot contribution missing")
	}
	if err := application.Run(); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("Boot job did not run and stop application")
	}
}

func TestConsulBaseAssemblyWithDisabledConsul(t *testing.T) {
	t.Setenv("DISABLE_CONSUL", "true")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("value: local"), 0o600); err != nil {
		t.Fatal(err)
	}
	custom := &fixtureCoordinator{}
	info := appinfo.New("test")
	files := fileconfig.PathList{filepath.Join(dir, "config.yaml")}
	for _, tc := range []struct {
		name  string
		want  job.ConcurrencyCoordinator
		build func() (*consulAssembly, func(), error)
	}{
		{"default", nil, func() (*consulAssembly, func(), error) { return initializeConsulBase(info, files, nil) }},
		{"custom", custom, func() (*consulAssembly, func(), error) {
			return initializeConsulCustomCoordinator(info, files, nil, custom)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, cleanup, err := tc.build()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if built.Coordinator != tc.want {
				t.Fatal("Coordinator selection was not preserved")
			}
			if built.Registrar != nil || built.Discovery != nil {
				t.Fatal("disabled Consul returned registry or discovery")
			}
			var value string
			if err := built.Config.Load("value", &value); err != nil {
				t.Fatal(err)
			}
			if value != "local" {
				t.Fatalf("local config = %q", value)
			}
			if err := built.App.Run(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 本用例只验证组装选择；分布式执行协议由 Job 测试覆盖。
type fixtureCoordinator struct{ job.ConcurrencyCoordinator }

func TestGeneratedAssemblyRollsBackOnAppConstructionFailure(t *testing.T) {
	previous := kratoslog.GetLogger()
	// 在应用冻结 Spec 时触发失败，验证此前构造的资源及全局 Logger 逆序回滚。
	built, cleanup, err := initialize(businessSources(t), "wire-test", 0, func(context.Context) context.Context { return nil })
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if err == nil || built != nil || cleanup != nil {
		t.Fatalf("failed assembly = (%v, cleanup=%v, %v)", built, cleanup != nil, err)
	}
	if kratoslog.GetLogger() != previous {
		t.Fatal("generated error rollback did not restore previous global logger")
	}
}
