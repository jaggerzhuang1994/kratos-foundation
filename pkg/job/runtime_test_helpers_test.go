package job

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

const runtimeTestWait = 5 * time.Second

func testModuleLog(t *testing.T) moduleLog {
	t.Helper()
	return moduleLog(testFoundationLogger(t))
}

func testFoundationLogger(t *testing.T) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  foundationlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: foundationlog.FileConfig{OutputConfig: foundationlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared)
}

func testFileModuleLog(t *testing.T) (moduleLog, string) {
	t.Helper()
	logger, path := testFileFoundationLogger(t)
	return moduleLog(logger), path
}

func testFileFoundationLogger(t *testing.T) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.log")
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: foundationlog.FileConfig{
			OutputConfig: foundationlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     foundationlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared), path
}

type testSchedule struct{ next time.Time }

func (s testSchedule) Next(time.Time) time.Time { return s.next }

type testParser struct {
	schedule scheduleSpec
	err      error
	calls    []string
}

func (p *testParser) Parse(string) (scheduleSpec, error) { return p.schedule, p.err }
func (p *testParser) ParseJob(name, spec string, immediate bool) (scheduleSpec, error) {
	p.calls = append(p.calls, name+":"+spec)
	return p.schedule, p.err
}

type scheduledCall struct {
	name     string
	job      Task
	schedule scheduleSpec
}
type testScheduler struct {
	mu            sync.Mutex
	calls         []scheduledCall
	starts, stops int
	started       chan struct{}
	startOnce     sync.Once
}

func (s *testScheduler) schedule(_ context.Context, name string, job Task, schedule scheduleSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, scheduledCall{name, job, schedule})
}
func (s *testScheduler) start() {
	s.mu.Lock()
	s.starts++
	if s.started == nil {
		s.started = make(chan struct{})
	}
	s.mu.Unlock()
	s.startOnce.Do(func() { close(s.started) })
}
func (s *testScheduler) stop() { s.mu.Lock(); defer s.mu.Unlock(); s.stops++ }
func (s *testScheduler) snapshot() ([]scheduledCall, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scheduledCall(nil), s.calls...), s.starts, s.stops
}
func (s *testScheduler) startedCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started == nil {
		s.started = make(chan struct{})
	}
	return s.started
}

type testGuard struct {
	ctx        context.Context
	releaseErr error
	releases   int
}

func (g *testGuard) Context() context.Context { return g.ctx }
func (g *testGuard) Release() error           { g.releases++; return g.releaseErr }

type testCoordinator struct {
	guard                  ExecutionGuard
	acquireErr, tryErr     error
	acquireCalls, tryCalls int
	key                    string
}

func (c *testCoordinator) Acquire(_ context.Context, key string) (ExecutionGuard, error) {
	c.acquireCalls++
	c.key = key
	return c.guard, c.acquireErr
}
func (c *testCoordinator) TryAcquire(_ context.Context, key string) (ExecutionGuard, error) {
	c.tryCalls++
	c.key = key
	return c.guard, c.tryErr
}

func waitFor(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(runtimeTestWait):
		t.Fatal("timed out")
	}
}

func waitForValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(runtimeTestWait):
		t.Fatal("timed out waiting for value")
		var zero T
		return zero
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(runtimeTestWait)
	defer timer.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("timed out waiting for condition")
		}
	}
}
