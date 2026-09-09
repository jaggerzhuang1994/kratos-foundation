package log

import (
	"bytes"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestSharedStateRotatingUpdatesReleaseWorkers(t *testing.T) {
	// 观察输出组件拥有的后台任务，不能用总 goroutine 数混入测试框架等噪声。
	workers := func() int {
		t.Helper()
		var stacks bytes.Buffer
		if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
			t.Fatal(err)
		}
		return strings.Count(stacks.String(), ".(*Logger).millRun(")
	}
	before := workers()
	config := Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std:        OutputConfig{Disable: true},
		File: FileConfig{
			Path:     filepath.Join(t.TempDir(), "app.log"),
			Rotating: RotatingConfig{MaxSize: 1, Compress: true, MaxFiles: 2},
		},
	}
	shared, cleanup, err := NewSharedState(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	logger := NewLogger(shared)
	for range 10 {
		if err := logger.Log(kratoslog.LevelInfo, "message", "before update"); err != nil {
			t.Fatal(err)
		}
		if err := shared.Update(config); err != nil {
			t.Fatal(err)
		}
	}
	// 候选在文件初始化阶段失败时也必须释放已启动的后台任务。
	invalid := config
	invalid.File.Path = filepath.Join(config.File.Path, "child.log")
	if err := shared.Update(invalid); err == nil {
		t.Fatal("Update() accepted a file as the parent directory")
	}
	if err := logger.Log(kratoslog.LevelInfo, "message", "after rejected update"); err != nil {
		t.Fatal(err)
	}
	cleanup()
	// Close 必须等待任务完成；让刚完成 defer 的 goroutine 退出运行时栈。
	runtime.Gosched()
	if after := workers(); after > before {
		t.Fatalf("rotating workers after cleanup = %d, before = %d", after, before)
	}
}
