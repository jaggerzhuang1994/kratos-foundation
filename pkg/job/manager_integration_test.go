package job

import (
	"context"
	"testing"
	"time"
)

func TestSpecOptionsAndMiddlewareDriveRealCronManager(t *testing.T) {
	observability := newRuntimeObservability(t)
	location := time.FixedZone("UTC+9", 9*60*60)
	var events []string
	middleware := func(next Handler) Handler {
		return func(ctx context.Context) error {
			events = append(events, "middleware:before")
			err := next(ctx)
			events = append(events, "middleware:after")
			return err
		}
	}
	run := make(chan struct{}, 1)
	spec := NewSpec().
		Middleware(nil, middleware).
		Option(
			nil,
			WithLocation(nil),
			WithLocation(location),
			WithTracing(false),
			WithMetrics(false),
			WithLogging(false),
		).
		RegisterCron("refresh", "@hourly", TaskFunc(func(context.Context) error {
			events = append(events, "task")
			run <- struct{}{}
			return nil
		}), RunImmediately(), WithConcurrentPolicy(DelayIfRunning)).(*Spec)
	manager, err := NewManager(
		observability.logger,
		spec,
		observability.tracingProvider,
		observability.metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager.options.Location != location || manager.options.TracingEnabled || manager.options.MetricsEnabled || manager.options.LoggingEnabled {
		t.Fatalf("manager options = %#v", manager.options)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()
	waitFor(t, run)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitForValue(t, startDone); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	want := []string{"middleware:before", "task", "middleware:after"}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
	}
}
