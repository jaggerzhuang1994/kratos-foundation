package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type cronFailure struct {
	ctxName string
	name    string
	err     error
}

func TestCronSchedulerRunsImmediateJobAndUsesConfiguredErrorHandler(t *testing.T) {
	logger, logPath := testFileFoundationLogger(t)
	cronLog := newCronLog(logger, managerOptions{LoggingEnabled: true})
	parser := newScheduleParser(cronLog)
	failures := make(chan cronFailure, 1)
	wantErr := errors.New("refresh failed")
	scheduler := newCron(
		cronLog,
		managerOptions{
			Location: time.UTC,
			ErrorHandler: func(ctx context.Context, name string, err error) {
				failures <- cronFailure{ctxName: JobNameFromContext(ctx), name: name, err: err}
			},
		},
		parser,
		newCronLogger(cronLog),
	)
	schedule, err := parser.ParseJob("refresh", "@hourly", true)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.schedule(
		context.Background(),
		"refresh",
		TaskFunc(func(context.Context) error { return wantErr }),
		schedule,
	)
	scheduler.start()
	failure := waitForValue(t, failures)
	scheduler.stop()

	if failure.name != "refresh" || failure.ctxName != "refresh" || !errors.Is(failure.err, wantErr) {
		t.Fatalf("cron failure = %#v", failure)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, message := range []string{"starting cron server", "stopping cron server", "cron server stopped"} {
		if !strings.Contains(logs, message) {
			t.Errorf("cron lifecycle log lacks %q: %s", message, logs)
		}
	}
}

func TestCronSchedulerDefaultErrorHandlerLogsJobFailure(t *testing.T) {
	logger, logPath := testFileFoundationLogger(t)
	cronLog := newCronLog(logger, managerOptions{LoggingEnabled: true})
	parser := newScheduleParser(cronLog)
	scheduler := newCron(cronLog, managerOptions{}, parser, newCronLogger(cronLog))
	schedule, err := parser.ParseJob("cleanup", "@daily", true)
	if err != nil {
		t.Fatal(err)
	}
	run := make(chan struct{})
	scheduler.schedule(context.Background(), "cleanup", TaskFunc(func(context.Context) error {
		close(run)
		return errors.New("cleanup failed")
	}), schedule)
	scheduler.start()
	waitFor(t, run)
	scheduler.stop()

	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, fragment := range []string{"cron job failed", "job=cleanup", "cleanup failed"} {
		if !strings.Contains(logs, fragment) {
			t.Errorf("default cron error log lacks %q: %s", fragment, logs)
		}
	}
}

func TestScheduleParserAcceptsOptionalSecondsAndDescriptors(t *testing.T) {
	parser := newScheduleParser(cronLog(testModuleLog(t)))
	for _, expression := range []string{"*/5 * * * * *", "@hourly"} {
		schedule, err := parser.Parse(expression)
		if err != nil {
			t.Fatalf("Parse(%q): %v", expression, err)
		}
		if next := schedule.Next(time.Now()); next.IsZero() {
			t.Fatalf("Parse(%q) returned a zero next time", expression)
		}
	}
}

func TestScheduleParserImmediateRunsOnlyOnce(t *testing.T) {
	parser := newScheduleParser(cronLog(testModuleLog(t)))
	spec, err := parser.ParseJob("report", "@hourly", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	if got := spec.Next(now); !got.Equal(now) {
		t.Fatalf("first Next = %v, want now", got)
	}
	if got := spec.Next(now); !got.After(now) {
		t.Fatalf("second Next = %v, want after now", got)
	}
	if _, err := parser.ParseJob("report", "bad cron", false); err == nil || !strings.Contains(err.Error(), "report") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestCronJobFiltersExpectedCancellation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	called := 0
	(&cronJob{ctx: canceled, name: "job", job: TaskFunc(func(context.Context) error { return context.Canceled }), errorHandler: func(context.Context, string, error) { called++ }}).Run()
	if called != 0 {
		t.Fatalf("cancellation handler calls = %d", called)
	}
	want := errors.New("failed")
	(&cronJob{ctx: context.Background(), name: "job", job: TaskFunc(func(context.Context) error { return want }), errorHandler: func(_ context.Context, name string, err error) {
		if name != "job" || !errors.Is(err, want) {
			t.Fatalf("handler = %q, %v", name, err)
		}
		called++
	}}).Run()
	if called != 1 {
		t.Fatalf("failure handler calls = %d", called)
	}
}
