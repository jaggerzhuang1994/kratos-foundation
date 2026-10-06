package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type cronFailure struct {
	ctx     context.Context
	ctxName string
	name    string
	err     error
}

type blockingStartupSchedule struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *blockingStartupSchedule) Next(time.Time) time.Time {
	// 暂停首条规则的计算，使后一条规则尚未提交时的启动日志可确定地被检测。
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return time.Time{}
}

func TestCronSchedulerLogsStartedAfterInitialRulesAreSubmitted(t *testing.T) {
	logger, logPath := testFileFoundationLogger(t)
	cronLog := newCronLog(logger, managerOptions{LoggingEnabled: true})
	parser := newScheduleParser(cronLog)
	scheduler := newCron(cronLog, managerOptions{}, parser, newCronLogger(cronLog))
	release := make(chan struct{})
	plan := &blockingStartupSchedule{entered: make(chan struct{}), release: release}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		scheduler.stop()
	})
	for _, name := range []string{"first", "second"} {
		scheduler.schedule(context.Background(), name, TaskFunc(func(context.Context) error {
			t.Error("zero-next schedule executed a task")
			return nil
		}), plan)
	}
	scheduler.start()
	waitFor(t, plan.entered)
	written, err := os.ReadFile(logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "event=job.cron.started") {
		t.Fatal("scheduler logged started before all initial rules were submitted")
	}
	close(release)
	waitUntil(t, func() bool {
		written, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(written), "event=job.cron.started")
	})
	updated := &blockingStartupSchedule{entered: make(chan struct{}), release: release}
	scheduler.reschedule("first", updated)
	waitFor(t, updated.entered)
	scheduler.stop()
	written, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(written), "event=job.cron.started"); count != 1 {
		t.Fatalf("scheduler startup logs = %d, want 1", count)
	}
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
				logger.WithContext(ctx).Info("cron error callback")
				failures <- cronFailure{ctx: ctx, ctxName: JobNameFromContext(ctx), name: name, err: err}
			},
		},
		parser,
		newCronLogger(cronLog),
	)
	schedule, err := parser.ParseJob("refresh", "@hourly", true)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(foundationlog.WithKv(context.Background(), "run.id", "scheduled"))
	defer cancel()
	scheduler.schedule(
		parent,
		"refresh",
		TaskFunc(func(ctx context.Context) error {
			logger.WithContext(ctx).Info("cron task invoked")
			return wantErr
		}),
		schedule,
	)
	scheduler.start()
	failure := waitForValue(t, failures)
	scheduler.stop()

	if failure.name != "refresh" || failure.ctxName != "refresh" || !errors.Is(failure.err, wantErr) {
		t.Fatalf("cron failure = %#v", failure)
	}
	cancel()
	if !errors.Is(failure.ctx.Err(), context.Canceled) {
		t.Fatalf("cron context lost parent cancellation: %v", failure.ctx.Err())
	}
	logger.WithContext(parent).Info("cron parent context")
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := string(written)
	for _, message := range []string{
		"event=job.cron.started", "event=job.cron.stopping", "event=job.cron.stopped",
		"msg=cron task invoked", "msg=cron error callback", "msg=cron parent context",
	} {
		if !strings.Contains(logs, message) {
			t.Errorf("cron lifecycle log lacks %q: %s", message, logs)
		}
	}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "msg=cron task invoked") || strings.Contains(line, "msg=cron error callback") {
			if !strings.Contains(line, "job=refresh") || !strings.Contains(line, "run.id=scheduled") {
				t.Errorf("cron context log lacks job or parent fields: %s", line)
			}
		}
		if strings.Contains(line, "msg=cron parent context") && (strings.Contains(line, "job=") || !strings.Contains(line, "run.id=scheduled")) {
			t.Errorf("cron changed parent context fields: %s", line)
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
	for _, fragment := range []string{"event=job.failed", "cron job failed", "job=cleanup", "cleanup failed"} {
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
