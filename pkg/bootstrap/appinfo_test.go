package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
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
	shared, cleanup, err := testlog.New(bootstrapLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	info := bootstrapAppInfo{id: "orders-1", name: "orders", version: "v1.2.3"}
	got, err := bootstrap.NewAppInfoBootstrap(app.NewSpec(), info)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.AppInfoBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if err := shared.Log(kratoslog.LevelInfo, "event", "started"); err != nil {
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
	shared, cleanup, err := testlog.New(bootstrapLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	spec := app.NewSpec()
	first := bootstrapAppInfo{id: "orders-1", name: "orders", version: "v1"}
	if _, err := bootstrap.NewAppInfoBootstrap(spec, first); err != nil {
		t.Fatal(err)
	}
	second := bootstrapAppInfo{id: "billing-1", name: "billing", version: "v2"}
	got, err := bootstrap.NewAppInfoBootstrap(spec, second)
	if err == nil || !strings.Contains(err.Error(), "register app info") ||
		!strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate bootstrap error = %v", err)
	}
	if got != (bootstrap.AppInfoBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	if err := shared.Log(kratoslog.LevelInfo, "event", "started"); err != nil {
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
