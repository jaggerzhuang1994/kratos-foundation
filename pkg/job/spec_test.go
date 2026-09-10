package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	).RegisterCron(
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
		{name: "empty name", spec: NewSpec().RegisterOnce("", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "nil task", spec: NewSpec().RegisterOnce("once", nil).(*Spec)},
		{name: "missing cron schedule", spec: NewSpec().RegisterCron("cron", "", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "duplicate names", spec: NewSpec().RegisterOnce("same", TaskFunc(func(context.Context) error { return nil })).RegisterDaemon("same", TaskFunc(func(context.Context) error { return nil })).(*Spec)},
		{name: "exit without once", spec: NewSpec().RegisterDaemon("daemon", TaskFunc(func(context.Context) error { return nil })).ExitWhenDone().(*Spec)},
		{name: "exit with background work", spec: NewSpec().RegisterOnce("once", TaskFunc(func(context.Context) error { return nil })).RegisterDaemon("daemon", TaskFunc(func(context.Context) error { return nil })).ExitWhenDone().(*Spec)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.spec.Validate(); err == nil {
				t.Fatal("Validate error = nil")
			}
		})
	}
	invalid := NewSpec().RegisterCron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithConcurrentPolicy(ConcurrentPolicy(99))).(*Spec)
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid policy accepted")
	}
	valid := NewSpec().RegisterCron("cron", "@hourly", TaskFunc(func(context.Context) error { return nil }), WithConcurrentPolicy(DelayIfDistributedRunning)).RegisterOnce("once", TaskFunc(func(context.Context) error { return nil })).(*Spec)
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}
