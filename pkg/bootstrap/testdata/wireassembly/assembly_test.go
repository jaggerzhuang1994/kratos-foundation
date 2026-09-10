package wireassembly

import (
	"context"
	"errors"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestGeneratedAssemblyAndCleanup(t *testing.T) {
	previous := kratoslog.GetLogger()
	built, cleanup, err := initialize(context.Background(), businessSources(t), "wire-test", 0)
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

func TestGeneratedAssemblyRollsBackOnAppConstructionFailure(t *testing.T) {
	previous := kratoslog.GetLogger()
	// Bootstrap 安装全局 Logger 后，nil Context 让 NewApp 失败并触发 Wire 逆序回滚。
	built, cleanup, err := initialize(nil, businessSources(t), "wire-test", 0)
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if err == nil || built != nil || cleanup != nil {
		t.Fatalf("failed assembly = (%v, cleanup=%v, %v)", built, cleanup != nil, err)
	}
	if err.Error() != "base context is nil" {
		t.Fatalf("assembly failed before reaching NewApp: %v", err)
	}
	if kratoslog.GetLogger() != previous {
		t.Fatal("generated error rollback did not restore previous global logger")
	}
}

func TestComponentsDeclaredInBootRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	application, cleanup, err := initializeComponents(ctx, businessSources(t), "boot-test")
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
