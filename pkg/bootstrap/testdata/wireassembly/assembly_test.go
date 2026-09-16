package wireassembly

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/registry/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestGeneratedAssemblyAndCleanup(t *testing.T) {
	previous := foundationlog.GetLogger()
	built, cleanup, err := initialize(businessSources(t), "wire-test", nil)
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
	func() {
		defer func() {
			if got := recover(); got != app.ErrSpecFrozen {
				t.Fatalf("Spec freeze panic = %v", got)
			}
		}()
		built.Spec.AddMetadata(nil)
		t.Fatal("expected frozen Spec panic")
	}()
	if foundationlog.GetLogger() == previous {
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
	if foundationlog.GetLogger() != previous {
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

func TestGeneratedAssemblyRollsBackOnAppConstructionFailure(t *testing.T) {
	previous := foundationlog.GetLogger()
	// 在应用冻结 Spec 时触发失败，验证此前构造的资源及全局 Logger 逆序回滚。
	built, cleanup, err := initialize(businessSources(t), "wire-test", func(context.Context) context.Context { return nil })
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if err == nil || built != nil || cleanup != nil {
		t.Fatalf("failed assembly = (%v, cleanup=%v, %v)", built, cleanup != nil, err)
	}
	if foundationlog.GetLogger() != previous {
		t.Fatal("generated error rollback did not restore previous global logger")
	}
}

func TestGeneratedDriverAssembly(t *testing.T) {
	t.Setenv("DISABLE_CONSUL", "true")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("tracing:\n  disable: true\nregistry:\n  instances:\n    default:\n      driver: consul\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info := appinfo.New("drivers")
	for _, tt := range []struct {
		name           string
		build          func(appinfo.AppInfo, bootstrap.LocalConfigPath) (*driverAssembly, func(), error)
		wantName       string
		wantConfigName string
	}{
		{"directory argument", func(info appinfo.AppInfo, path bootstrap.LocalConfigPath) (*driverAssembly, func(), error) {
			return initializeDrivers(info, path, "orders")
		}, "orders", info.Name()},
		{"directory provider", initializeDriversWithDirectoryProvider, "shared-orders", info.Name()},
		{"name provider", initializeDriversWithNameProvider, "shared-orders", "shared-config"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			application, cleanup, err := tt.build(info, bootstrap.LocalConfigPath(path))
			if err != nil {
				t.Fatal(err)
			}
			if string(application.Directory) != tt.wantName {
				t.Fatalf("config directory = %q, want %q", application.Directory, tt.wantName)
			}
			if string(application.Name) != tt.wantConfigName || application.App.Name() != info.Name() {
				t.Fatalf("config name=%q app name=%q", application.Name, application.App.Name())
			}
			cleanup()
			cleanup()
			if application.App == nil {
				t.Fatal("nil app")
			}
			if _, _, _, err := application.Client.AcquireClient(context.Background(), "missing"); !errors.Is(err, client.ErrFactoryClosed) {
				t.Fatalf("client cleanup: %v", err)
			}
			var value string
			if err := application.Config.Load("missing", &value); !errors.Is(err, config.ErrManagerClosed) {
				t.Fatalf("config cleanup: %v", err)
			}

		})
	}
}
