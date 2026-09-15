package bootstrap_test

import (
	"context"
	"fmt"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func bootstrapLogConfig(path string) testlog.Config {
	return testlog.Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: false,
		TimeFormat:  time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelInfo},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	}
}

type logBootstrapAppInfo struct{}

func (logBootstrapAppInfo) ID() string                  { return "bootstrap-id" }
func (logBootstrapAppInfo) Name() string                { return "bootstrap-name" }
func (logBootstrapAppInfo) Version() string             { return "bootstrap-version" }
func (logBootstrapAppInfo) Metadata() map[string]string { return nil }

func TestBootstrapRegistersStableLoggerAndRestoresPreviousGlobal(t *testing.T) {
	shared, release, err := testlog.New(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	got, cleanup, err := bootstrap.NewLogBootstrap(app.NewSpec(), testconfig.Empty(t), shared)
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
	if global == shared {
		t.Fatal("global installation must retain its own cleanup identity")
	}
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
	path := filepath.Join(t.TempDir(), "global.log")
	shared, release, err := testlog.New(bootstrapLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	original := foundationlog.GetLogger()
	previous := kratoslog.NewStdLogger(io.Discard)
	foundationlog.SetLogger(previous)
	t.Cleanup(func() { foundationlog.SetLogger(original) })

	spec := app.NewSpec()
	_, cleanup, err := bootstrap.NewLogBootstrap(spec, testconfig.Empty(t), shared)
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
	policy, releasePolicy, err := app.NewStopPolicy(config, manager, installed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releasePolicy)
	if _, err := app.NewApp(context.Background(), spec, config, policy, nil); err != nil {
		t.Fatal(err)
	}
	if foundationlog.GetLogger() != installed {
		t.Fatal("NewApp replaced the logger installed by log bootstrap")
	}

	kratoslog.Info("framework-after-app")
	foundationlog.Infow("module", "bootstrap", "msg", "foundation-after-app")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if strings.Contains(line, "framework-after-app") && !strings.Contains(line, "module=kratos") {
			t.Fatalf("framework ownership lost: %s", line)
		}
		if strings.Contains(line, "foundation-after-app") && !strings.Contains(line, "module=bootstrap") {
			t.Fatalf("foundation ownership lost: %s", line)
		}
	}
	if !strings.Contains(string(content), "framework-after-app") || !strings.Contains(string(content), "foundation-after-app") {
		t.Fatalf("missing events: %s", content)
	}

	cleanup()
	if foundationlog.GetLogger() != previous {
		t.Fatal("log bootstrap cleanup did not restore the previous logger after NewApp")
	}
}

func TestBootstrapRegistrationFailureLeavesGlobalLoggerUntouched(t *testing.T) {
	shared, release, err := testlog.New(bootstrapConfig())
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
	got, cleanup, err := bootstrap.NewLogBootstrap(spec, testconfig.Empty(t), shared)
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

func bootstrapConfig() testlog.Config {
	return testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{
				Disable: true,
				Level:   kratoslog.LevelInfo,
			},
		},
	}
}

func TestGlobalAndInjectedLogCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caller.log")
	shared, release, err := testlog.New(integrationLogConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, cleanup, err := bootstrap.NewLogBootstrap(app.NewSpec(), testconfig.Empty(t), shared)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, _, line, _ := runtime.Caller(0)
	kratoslog.Infof("global caller")
	globalLine := line + 1
	logger := shared
	_, _, line, _ = runtime.Caller(0)
	logger.Info("injected caller")
	injectedLine := line + 1
	release()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected logs: %s", data)
	}
	for i, want := range []int{globalLine, injectedLine} {
		if !strings.Contains(lines[i], fmt.Sprintf("caller=bootstrap/log_test.go:%d", want)) {
			t.Fatalf("caller should point to line %d: %s", want, lines[i])
		}
	}
}

func TestLogBootstrapHotReloadsExistingViews(t *testing.T) {
	cfg := bootstrapConfig()
	cfg.Level = kratoslog.LevelDebug
	cfg.File.Path = filepath.Join(t.TempDir(), "runtime.log")
	cfg.File.Disable = false
	cfg.File.Level = kratoslog.LevelDebug
	cfg.File.Rotating.Disable = true
	logger, release, err := testlog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	source := testconfig.NewMutableSource(t, "log", &config_pb.Logging{File: &config_pb.LogFilePolicy{Level: proto.String("error")}, Modules: []*config_pb.LogModule{{Module: "orders", Level: proto.String("error")}}})
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManager()
	_, cancel, err := bootstrap.NewLogBootstrap(app.NewSpec(), manager, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer func() {
		if err := foundationlog.ApplyRuntimeConfig(&foundationlog.RuntimeConfig{}); err != nil {
			t.Error(err)
		}
	}()
	view := logger.WithModule("orders").WithLevel(kratoslog.LevelError)
	view.Info("before-hidden")
	source.Update(t, &config_pb.Logging{File: &config_pb.LogFilePolicy{Level: proto.String("debug")}, Modules: []*config_pb.LogModule{{Module: "orders", Level: proto.String("debug")}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		view.Debug("after-visible")
		body, err := os.ReadFile(cfg.File.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "after-visible") {
			if strings.Contains(string(body), "before-hidden") {
				t.Fatalf("logs=%s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("existing logger did not adopt config")
		}
		time.Sleep(time.Millisecond)
	}
}

type logPolicyManager struct {
	config.Manager
	callback              config.Observer
	loadErr, subscribeErr error
	cancelled             bool
}

func (m *logPolicyManager) Load(_ string, target any, _ ...any) error {
	if m.loadErr != nil {
		return m.loadErr
	}
	*target.(*foundationlog.RuntimeConfig) = foundationlog.RuntimeConfig{}
	return nil
}
func (m *logPolicyManager) Subscribe(key string, _ any, observer config.Observer, _ ...any) (func(), error) {
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	m.callback = observer
	return func() { m.cancelled = true }, nil
}

func TestLogBootstrapPolicyFailureBoundaries(t *testing.T) {
	logger, cleanup, err := testlog.New(bootstrapConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, tc := range []struct {
		name      string
		manager   *logPolicyManager
		cancelled bool
	}{
		{name: "load fails", manager: &logPolicyManager{loadErr: fmt.Errorf("load failed")}},
		{name: "subscribe fails", manager: &logPolicyManager{subscribeErr: fmt.Errorf("subscribe failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cancel, err := bootstrap.NewLogBootstrap(app.NewSpec(), tc.manager, logger)
			if err == nil || cancel != nil {
				t.Fatal("invalid initial policy accepted")
			}
			if tc.manager.cancelled != tc.cancelled {
				t.Fatal("subscription cleanup mismatch")
			}
		})
	}
}

func TestLogBootstrapRejectsUpdateAndRestoresDefaults(t *testing.T) {
	cfg := bootstrapConfig()
	cfg.File.Path = filepath.Join(t.TempDir(), "updates.log")
	cfg.File.Disable = false
	cfg.File.Level = kratoslog.LevelDebug
	cfg.File.Rotating.Disable = true
	cfg.Level = kratoslog.LevelDebug
	cfg.File.Level = kratoslog.LevelInfo
	logger, release, err := testlog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	manager := &logPolicyManager{}
	_, cancel, err := bootstrap.NewLogBootstrap(app.NewSpec(), manager, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	manager.callback("log", &foundationlog.RuntimeConfig{File: &foundationlog.FilePolicy{Level: proto.String("debug")}}, nil)
	manager.callback("log", &foundationlog.RuntimeConfig{File: &foundationlog.FilePolicy{Level: proto.String("invalid")}}, nil)
	logger.Debug("previous-policy")
	manager.callback("log", nil, fmt.Errorf("source failed"))
	logger.Debug("source-failure-kept-policy")
	manager.callback("log", &foundationlog.RuntimeConfig{}, nil)
	logger.Debug("deleted-policy-hidden")
	cancel()
	if !manager.cancelled {
		t.Fatal("cleanup did not cancel subscription")
	}
	body, err := os.ReadFile(cfg.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "previous-policy") || !strings.Contains(string(body), "source-failure-kept-policy") || strings.Contains(string(body), "deleted-policy-hidden") {
		t.Fatalf("policy transition logs=%s", body)
	}
}
