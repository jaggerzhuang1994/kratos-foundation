package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
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
		middlewareChain{loggingMiddleware(log), recoveryMiddleware()},
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
