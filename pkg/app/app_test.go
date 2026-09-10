package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
)

type newAppTestInfo struct{}

func (newAppTestInfo) ID() string                  { return "test-id" }
func (newAppTestInfo) Name() string                { return "test-name" }
func (newAppTestInfo) Version() string             { return "test-version" }
func (newAppTestInfo) Metadata() map[string]string { return nil }

func newAppTestConfigAndPolicy(t testing.TB) (Config, *StopPolicy) {
	t.Helper()
	manager := testconfig.Empty(t)
	config, err := NewConfig(manager)
	if err != nil {
		t.Fatal(err)
	}
	policy, cleanup, err := NewStopPolicy(
		config,
		manager,
		kratoslog.NewStdLogger(io.Discard),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return config, policy
}

func registerNewAppTestInfo(t testing.TB, spec *Spec) {
	t.Helper()
	if err := spec.RegisterAppInfo(newAppTestInfo{}); err != nil {
		t.Fatal(err)
	}
}

func registerNewAppTestLogger(t testing.TB, spec *Spec) {
	t.Helper()
	if err := spec.RegisterLogger(kratoslog.NewStdLogger(io.Discard)); err != nil {
		t.Fatal(err)
	}
}

func TestNewAppRejectsMissingAppInfo(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestLogger(t, spec)

	app, err := NewApp(context.Background(), spec, config, policy, nil)
	if app != nil || err == nil || !strings.Contains(err.Error(), "app info") {
		t.Fatalf("NewApp(missing app info) = (%v, %v), want explicit app info error", app, err)
	}
}

func TestNewAppRejectsMissingLogger(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)

	app, err := NewApp(context.Background(), spec, config, policy, nil)
	if app != nil || err == nil || !strings.Contains(err.Error(), "logger") {
		t.Fatalf("NewApp(missing logger) = (%v, %v), want explicit logger error", app, err)
	}
}

func TestNewAppConstructsApplicationFromSpec(t *testing.T) {
	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)
	registerNewAppTestLogger(t, spec)

	application, err := NewApp(context.Background(), spec, config, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if application.App == nil {
		t.Fatal("App does not own a Kratos application")
	}
	if application.ID() != "test-id" || application.Name() != "test-name" {
		t.Fatalf("application identity = %q/%q", application.ID(), application.Name())
	}
	if err := spec.AddMetadata(map[string]string{"late": "value"}); !errors.Is(err, ErrSpecFrozen) {
		t.Fatalf("post-NewApp registration error = %v, want ErrSpecFrozen", err)
	}
}

func TestNewAppRestoresKratosGlobalLoggerAfterConstruction(t *testing.T) {
	original := kratoslog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	registered := kratoslog.NewStdLogger(io.Discard)
	kratoslog.SetLogger(previous)
	t.Cleanup(func() { kratoslog.SetLogger(original) })

	config, policy := newAppTestConfigAndPolicy(t)
	spec := NewSpec()
	registerNewAppTestInfo(t, spec)
	if err := spec.RegisterLogger(registered); err != nil {
		t.Fatal(err)
	}
	if _, err := NewApp(context.Background(), spec, config, policy, nil); err != nil {
		t.Fatal(err)
	}
	if got := kratoslog.GetLogger(); got != previous {
		t.Fatalf("global logger after NewApp has type %T, want previous %T", got, previous)
	}
}

func TestOnceStopSharesOneResultAcrossCallers(t *testing.T) {
	want := errors.New("stop failed")
	calls := 0
	release := make(chan struct{})
	stop := onceStop(func() error {
		calls++
		<-release
		return want
	})

	results := make(chan error, 2)
	go func() { results <- stop() }()
	go func() { results <- stop() }()
	close(release)
	for range 2 {
		if err := <-results; !errors.Is(err, want) {
			t.Fatalf("stop error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("stop calls = %d, want 1", calls)
	}
}
