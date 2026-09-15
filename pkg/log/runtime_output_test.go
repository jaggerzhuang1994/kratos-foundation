package log

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"google.golang.org/protobuf/proto"
)

func runtimeEnvironment(path string) envConfig {
	return envConfig{Level: kratoslog.LevelDebug, TimeFormat: time.RFC3339, MsgKey: "message", Std: outputConfig{Disable: true}, File: fileConfig{outputConfig: outputConfig{Disable: true, Level: kratoslog.LevelDebug}, Path: path, Rotating: rotatingConfig{MaxSize: 100}}}
}

func TestRuntimeFileLifecycleAndRollback(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	original := filepath.Join(t.TempDir(), "original.log")
	base := runtimeEnvironment(original)
	base.Level = kratoslog.LevelInfo
	base.FilterEmpty = true
	baseLogger, cleanup, err := newLogger(state, base)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	view := baseLogger.WithContext(request.WithDebug(context.Background())).WithFilterKeys("instance.secret")
	view.Info("disabled")
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("default opened file: %v", err)
	}
	if err := state.applyRuntimeConfig(&RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true)}}); err != nil {
		t.Fatal(err)
	}
	view.With("empty", "", "instance.secret", "hidden").Debug("first")
	old := baseLogger.(*logger).config.output.(*outputLogger).preparedOutput
	if err := state.applyRuntimeConfig(&RuntimeConfig{FilterKeys: []string{}, File: &FilePolicy{Enable: proto.Bool(true), Level: proto.String("warn")}}); err != nil {
		t.Fatal(err)
	}
	if old.file != baseLogger.(*logger).config.output.(*outputLogger).file {
		t.Fatal("policy-only update reopened file")
	}
	view.Info("filtered")
	bad := t.TempDir()
	if err := state.applyRuntimeConfig(&RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true), Path: &bad}}); err == nil {
		t.Fatal("bad path accepted")
	}
	view.Error("kept")
	path := filepath.Join(t.TempDir(), "next.log")
	size, age, files := 1, 2, 3
	policy := &RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true), Path: &path, Level: proto.String("debug"), FilterKeys: []string{}, Rotating: &RotatingPolicy{Disable: proto.Bool(false), MaxSize: &size, MaxFileAge: &age, MaxFiles: &files, LocalTime: proto.Bool(true), Compress: proto.Bool(true)}}}
	if err := state.applyRuntimeConfig(policy); err != nil {
		t.Fatal(err)
	}
	view.Debug("next")
	if err := state.applyRuntimeConfig(&RuntimeConfig{}); err != nil {
		t.Fatal(err)
	}
	view.Error("disabled-again")
	cleanup()
	cleanup()
	if err := view.Log(kratoslog.LevelError, "message", "closed"); err == nil {
		t.Fatal("closed logger wrote")
	}
	for file, wants := range map[string][]string{original: {"message=first", "message=kept"}, path: {"message=next"}} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(body), want) {
				t.Fatalf("missing %s: %s", want, body)
			}
		}
		for _, absent := range []string{"filtered", "disabled-again", "hidden", "empty="} {
			if strings.Contains(string(body), absent) {
				t.Fatalf("unexpected %s: %s", absent, body)
			}
		}
	}
}

func TestRuntimeOutputInheritanceAndMergedValidation(t *testing.T) {
	base := runtimeEnvironment("app.log")
	base.Std = outputConfig{Disable: true, Level: kratoslog.LevelWarn, FilterKeys: []string{"secret"}}
	config, err := mergeOutputConfig(base, &RuntimeConfig{Std: &OutputPolicy{Disable: proto.Bool(false), Level: proto.String("debug"), FilterKeys: []string{}}})
	if err != nil || config.Std.Disable || config.Std.Level != kratoslog.LevelDebug || len(config.Std.FilterKeys) != 0 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	zero := 0
	if _, err := mergeOutputConfig(base, &RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true), Rotating: &RotatingPolicy{MaxSize: &zero}}}); err == nil {
		t.Fatal("enabled rotation accepted zero size")
	}
	state := &sharedState{}
	if err := state.applyRuntimeConfig(&RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true), Rotating: &RotatingPolicy{MaxSize: &zero}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newLogger(state, base); err == nil {
		t.Fatal("new instance ignored active invalid-for-env policy")
	}
}

func TestRuntimeConcurrentUpdatesWritesAndCleanup(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	base := runtimeEnvironment(filepath.Join(t.TempDir(), "concurrent.log"))
	base.File.Disable = false
	base.File.Rotating.Disable = true
	baseLogger, cleanup, err := newLogger(state, base)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var wg sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if err := state.applyRuntimeConfig(&RuntimeConfig{FilterKeys: []string{"secret"}, Modules: []ModulePolicy{{Module: "*", Level: proto.String("info"), FilterKeys: []string{"module_secret"}}}, File: &FilePolicy{Level: proto.String("debug"), Rotating: &RotatingPolicy{Disable: proto.Bool(i%2 == 0)}}}); err != nil {
					t.Error(err)
				}
				baseLogger.WithModule("worker").With("secret", "hidden", "module_secret", "module-hidden").Info("event")
			}
		}()
	}
	wg.Wait()
	cleanup()
	if err := state.applyRuntimeConfig(&RuntimeConfig{File: &FilePolicy{Enable: proto.Bool(true)}}); err != nil {
		t.Fatal(err)
	}
	if len(state.owners) != 0 {
		t.Fatal("cleanup left registered output")
	}
	body, err := os.ReadFile(base.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "message=event"); got != 90 {
		t.Fatalf("concurrent file switches lost writes: %d", got)
	}
	if strings.Contains(string(body), "hidden") {
		t.Fatal("mixed filter snapshot")
	}
}

func TestRuntimeUpdateRejectsAllOwnersWhenOneMergeFails(t *testing.T) {
	state := &sharedState{}
	state.custom.Store(&customState{})
	firstEnv := runtimeEnvironment(filepath.Join(t.TempDir(), "first.log"))
	firstEnv.File.Disable = false
	first, closeFirst, err := newLogger(state, firstEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFirst()
	secondEnv := runtimeEnvironment(filepath.Join(t.TempDir(), "second.log"))
	secondEnv.File.Rotating.MaxSize = 0 // 禁用时合法，启用轮转后非法。
	_, closeSecond, err := newLogger(state, secondEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSecond()
	previous := state.custom.Load()
	if err := state.applyRuntimeConfig(&RuntimeConfig{FilterKeys: []string{"message"}, File: &FilePolicy{Enable: proto.Bool(true)}}); err == nil {
		t.Fatal("accepted invalid merged owner")
	}
	if state.custom.Load() != previous {
		t.Fatal("partially committed policy")
	}
	first.Info("still-active")
	data, err := os.ReadFile(firstEnv.File.Path)
	if err != nil || !strings.Contains(string(data), "message=still-active") {
		t.Fatalf("active owner changed: %s %v", data, err)
	}
}
