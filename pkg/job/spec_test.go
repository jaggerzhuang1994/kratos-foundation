package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSpecRequiresCoordinatorForDistributedPolicy(t *testing.T) {
	spec := NewSpec()
	spec.Cron(
		"cleanup",
		"@every 1m",
		TaskFunc(func(context.Context) error { return nil }),
		WithConcurrentPolicy(SkipIfDistributedRunning),
	)
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate accepted distributed policy without coordinator")
	}
}

func TestPublicManagerAndCronOptionsDriveImmediateFailureHandling(t *testing.T) {
	wantErr := errors.New("task failed")
	reported := make(chan error, 1)
	spec := NewSpec()
	spec.Option(
		WithLocation(time.UTC),
		WithTracing(false),
		WithMetrics(false),
		WithLogging(false),
		WithErrorHandler(func(_ context.Context, name string, err error) {
			if name != "report" {
				reported <- errors.New("unexpected job name: " + name)
				return
			}
			reported <- err
		}),
	).Cron(
		"report",
		"@hourly",
		TaskFunc(func(context.Context) error { return wantErr }),
		RunImmediately(),
	)

	manager := newTestManager(t, spec)
	started := make(chan error, 1)
	go func() { started <- manager.Start(context.Background()) }()
	select {
	case got := <-reported:
		if !errors.Is(got, wantErr) {
			t.Fatalf("error handler received %v, want %v", got, wantErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("immediate cron task did not report its failure")
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job manager did not stop")
	}
}

// These cases kill mutations that accept incomplete definitions, permit invalid
// concurrency policies, or let ExitWhenDone coexist with background work.
func TestSpecValidateRejectsInvalidDefinitions(t *testing.T) {
	for _, test := range []struct {
		name string
		spec *Spec
	}{
		{name: "empty name", spec: NewSpec().Once("", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "nil task", spec: NewSpec().Once("once", nil).(*Spec)},
		{name: "missing cron schedule", spec: NewSpec().Cron("cron", "", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "duplicate names", spec: NewSpec().Once("same", TaskFunc(func(context.Context) error { return nil })).Daemon("same", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "distributed without coordinator", spec: NewSpec().Cron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithConcurrentPolicy(SkipIfDistributedRunning)).(*Spec)},
		{name: "exit without once", spec: NewSpec().Daemon("daemon", TaskFunc(func(context.Context) error { return nil })).ExitWhenDone().(*Spec)},
		{name: "exit with background work", spec: NewSpec().Once("once", TaskFunc(func(context.Context) error { return nil })).Daemon("daemon", TaskFunc(func(context.Context) error { return nil })).ExitWhenDone().(*Spec)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.spec.Validate(); err == nil {
				t.Fatal("Validate error = nil")
			}
		})
	}
	invalid := NewSpec().Cron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithConcurrentPolicy(ConcurrentPolicy(99))).(*Spec)
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid policy accepted")
	}
	valid := NewSpec().Coordinator(&testCoordinator{}).Cron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithConcurrentPolicy(DelayIfDistributedRunning)).Once("once", TaskFunc(func(context.Context) error { return nil })).(*Spec)
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}
