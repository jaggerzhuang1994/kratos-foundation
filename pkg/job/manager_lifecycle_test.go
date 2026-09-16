package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// These tests kill mutations that classify task kinds incorrectly, delay Cron
// startup behind an external barrier, or leave Done open after an ExitWhenDone run.
func TestNewManagerClassifiesAndSchedulesJobs(t *testing.T) {
	spec := NewSpec().RegisterCron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil })).RegisterOnce("once", TaskFunc(func(context.Context) error { return nil })).RegisterDaemon("daemon", TaskFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })).(*Spec)
	parser := &testParser{schedule: testSchedule{next: time.Now().Add(time.Hour)}}
	scheduler := &testScheduler{}
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), scheduler, parser, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.HasJobs() || manager.IsOneShot() || len(manager.cronJobs) != 1 || len(manager.onceJobs) != 1 || len(manager.daemonJobs) != 1 || len(parser.calls) != 1 {
		t.Fatalf("compiled manager = %#v, parser=%#v", manager, parser)
	}

	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitFor(t, scheduler.startedCh())
	calls, _, _ := scheduler.snapshot()
	if len(calls) != 1 || calls[0].name != "cron" {
		t.Fatalf("scheduled calls = %#v", calls)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	_, _, stops := scheduler.snapshot()
	if stops != 1 {
		t.Fatalf("scheduler stops = %d", stops)
	}
}

func TestManagerExitWhenDoneRequestsApplicationStopAndClosesDone(t *testing.T) {
	run := 0
	spec := NewSpec().RegisterOnce("migrate", TaskFunc(func(context.Context) error { run++; return nil })).ExitWhenDone().(*Spec)
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), &testScheduler{}, &testParser{schedule: testSchedule{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.IsOneShot() {
		t.Fatal("ExitWhenDone not retained")
	}
	err = manager.Start(context.Background())
	if err != ErrCompleted {
		t.Fatalf("Start error = %v, want ErrStopRequested", err)
	}
	if run != 1 {
		t.Fatalf("runs = %d, want 1", run)
	}
	waitFor(t, manager.done)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerDaemonFailureStopsApplication(t *testing.T) {
	want := errors.New("daemon failed")
	spec := NewSpec().RegisterDaemon("worker", TaskFunc(func(context.Context) error { return want })).(*Spec)
	var observed error
	spec.Option(WithErrorHandler(func(_ context.Context, name string, err error) {
		if name != "worker" {
			t.Errorf("handler name = %q", name)
		}
		observed = err
	}))
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), &testScheduler{}, &testParser{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Start(context.Background())
	if !errors.Is(err, want) || !errors.Is(observed, want) {
		t.Fatalf("Start = %v, observed=%v", err, observed)
	}
	waitFor(t, manager.done)
}

func TestManagerStartRejectsDuplicateStart(t *testing.T) {
	spec := NewSpec().RegisterDaemon("wait", TaskFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })).(*Spec)
	manager, err := newManager(testModuleLog(t), nil, spec, newManagerOptions(spec), &testScheduler{}, &testParser{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		started := manager.started
		return started
	})
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("second Start error = nil")
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("first Start error = %v", err)
	}
}

func TestManagerParentCancellationBeforeStartClosesRuntime(t *testing.T) {
	runs := 0
	spec := NewSpec().RegisterDaemon("worker", TaskFunc(func(context.Context) error {
		runs++
		return nil
	})).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context canceled", err)
	}
	waitFor(t, manager.done)
	if runs != 0 {
		t.Fatalf("runs before application start = %d, want 0", runs)
	}
}

func TestManagerStopUnblocksStart(t *testing.T) {
	ran := make(chan struct{})
	spec := NewSpec().RegisterOnce("migration", TaskFunc(func(context.Context) error {
		close(ran)
		return nil
	})).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitUntil(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		return manager.started
	})
	waitFor(t, ran)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
}

func TestManagerParentCancellationStopsRunningDaemon(t *testing.T) {
	started := make(chan struct{})
	spec := NewSpec().RegisterDaemon("worker", TaskFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(ctx) }()
	waitFor(t, started)
	cancel()
	if err := waitForValue(t, startDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context canceled", err)
	}
	waitFor(t, manager.done)
}

func TestManagerParentCancellationStopsOneShotWait(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	spec := NewSpec().RegisterOnce("migration", TaskFunc(func(ctx context.Context) error {
		close(started)
		<-release
		return ctx.Err()
	})).ExitWhenDone().(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(ctx) }()
	waitFor(t, started)
	cancel()
	if err := waitForValue(t, startDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context canceled", err)
	}
	close(release)
	waitFor(t, manager.done)
}

func TestManagerStopBeforeStartPreventsLaterLaunch(t *testing.T) {
	runs := 0
	spec := NewSpec().RegisterOnce("migration", TaskFunc(func(context.Context) error {
		runs++
		return nil
	})).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("runs after pre-start Stop = %d, want 0", runs)
	}
}

func TestManagerStartJobsRefusesWorkAfterShutdownBegins(t *testing.T) {
	spec := NewSpec().RegisterOnce("migration", TaskFunc(func(context.Context) error { return nil })).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if onceDone, daemonErrors, launched := manager.startJobs(context.Background()); launched || onceDone != nil || daemonErrors != nil {
		t.Fatalf("startJobs after Stop = (%v, %v, %t)", onceDone, daemonErrors, launched)
	}
}

func TestManagerStopHonorsContextWhileUncooperativeTaskFinishes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	spec := NewSpec().RegisterDaemon("worker", TaskFunc(func(context.Context) error {
		close(started)
		<-release
		return nil
	})).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitFor(t, started)

	stopCtx, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	if err := manager.Stop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop error = %v, want context canceled", err)
	}
	close(release)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
}

func TestManagerReportsOnceFailuresWithoutStoppingApplication(t *testing.T) {
	wantErr := errors.New("migration failed")
	reported := make(chan struct {
		name string
		err  error
	}, 1)
	spec := NewSpec().
		Option(WithErrorHandler(func(_ context.Context, name string, err error) {
			reported <- struct {
				name string
				err  error
			}{name: name, err: err}
		})).
		RegisterOnce("migration", TaskFunc(func(context.Context) error { return wantErr })).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	got := waitForValue(t, reported)
	if got.name != "once" || !errors.Is(got.err, wantErr) || !strings.Contains(got.err.Error(), "migration") {
		t.Fatalf("reported once failure = %#v", got)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
}

func TestManagerDefaultErrorHandlerLogsOnceFailure(t *testing.T) {
	log, logPath := testFileModuleLog(t)
	spec := NewSpec().RegisterOnce("migration", TaskFunc(func(context.Context) error {
		return errors.New("migration failed")
	})).(*Spec)
	manager, err := newManager(
		log,
		middlewareChain{loggingMiddleware(log)},
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitUntil(t, func() bool {
		written, readErr := os.ReadFile(logPath)
		return readErr == nil && strings.Contains(string(written), "job failed")
	})
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	if strings.Count(logs, "job failed") != 1 || strings.Contains(logs, "job execution failed") {
		t.Fatalf("duplicated final failure: %s", logs)
	}
	for _, fragment := range []string{"job=once", "migration: migration failed", "job failed"} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("default job error log lacks %q: %s", fragment, logs)
		}
	}
}

func TestManagerTreatsUnexpectedSuccessfulDaemonExitAsFailure(t *testing.T) {
	reported := make(chan error, 1)
	spec := NewSpec().
		Option(WithErrorHandler(func(_ context.Context, _ string, err error) { reported <- err })).
		RegisterDaemon("worker", TaskFunc(func(context.Context) error { return nil })).(*Spec)
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "daemon exited without cancellation") {
		t.Fatalf("Start error = %v", err)
	}
	if got := waitForValue(t, reported); !strings.Contains(got.Error(), "daemon exited without cancellation") {
		t.Fatalf("reported daemon error = %v", got)
	}
}

func TestNewManagerReturnsCronParseFailureWithoutStartingScheduler(t *testing.T) {
	wantErr := errors.New("invalid schedule")
	spec := NewSpec().RegisterCron("report", "bad", TaskFunc(func(context.Context) error { return nil })).(*Spec)
	scheduler := &testScheduler{}
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		scheduler,
		&testParser{err: wantErr},
		nil,
	)
	if manager != nil || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "report") {
		t.Fatalf("newManager() = (%v, %v)", manager, err)
	}
	if calls, starts, stops := scheduler.snapshot(); len(calls) != 0 || starts != 0 || stops != 0 {
		t.Fatalf("scheduler changed after parse failure: calls=%v starts=%d stops=%d", calls, starts, stops)
	}
}

func TestManagerWithNoJobsStartsAndStopsCleanly(t *testing.T) {
	spec := NewSpec()
	manager, err := newManager(
		testModuleLog(t),
		nil,
		spec,
		newManagerOptions(spec),
		&testScheduler{},
		&testParser{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

const runtimeTestWait = 5 * time.Second

func testModuleLog(t *testing.T) moduleLog {
	t.Helper()
	return moduleLog(testFoundationLogger(t))
}

func testFoundationLogger(t *testing.T) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
}

func testFileModuleLog(t *testing.T) (moduleLog, string) {
	t.Helper()
	logger, path := testFileFoundationLogger(t)
	return moduleLog(logger), path
}

func testFileFoundationLogger(t *testing.T) (foundationlog.Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.log")
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelDebug,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelDebug,
		},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Level: kratoslog.LevelDebug},
			Path:         path,
			Rotating:     testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared, path
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
func (s *testScheduler) reschedule(name string, schedule scheduleSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.calls {
		if s.calls[i].name == name {
			s.calls[i].schedule = schedule
		}
	}
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
