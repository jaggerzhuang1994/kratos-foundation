package app

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// These tests kill mutations that run start hooks after an error, skip later
// cleanup hooks, or expose Spec's hook storage directly.
func TestAppHooksHooksOrderFailureAndCleanupAggregation(t *testing.T) {
	var calls []string
	fail := errors.New("start failed")
	snapshot := appSnapshot{
		beforeStart: []HookFunc{
			func(context.Context) error { calls = append(calls, "first"); return nil },
			func(context.Context) error { calls = append(calls, "fail"); return fail },
			func(context.Context) error { calls = append(calls, "never"); return nil },
		},
		beforeStop: []HookFunc{
			func(context.Context) error { calls = append(calls, "before-stop"); return errors.New("before") },
		},
		afterStop: []HookFunc{
			func(context.Context) error { calls = append(calls, "after-one"); return errors.New("after-one") },
			func(context.Context) error { calls = append(calls, "after-two"); return errors.New("after-two") },
		},
	}
	runner := newApp(snapshot, newStaticStopPolicy(time.Second))
	if err := runner.runBeforeStart(context.Background()); !errors.Is(err, fail) {
		t.Fatalf("before start = %v", err)
	}
	if want := []string{"first", "fail"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("start calls = %#v", calls)
	}
	err := runner.runAfterStop(context.Background())
	if !stringsContain(err.Error(), "before") || !stringsContain(err.Error(), "after-one") || !stringsContain(err.Error(), "after-two") {
		t.Fatalf("cleanup error = %v", err)
	}
	if want := []string{"first", "fail", "before-stop", "after-one", "after-two"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("all calls = %#v", calls)
	}
	_ = runner.runAfterStop(context.Background())
	if len(calls) != 5 {
		t.Fatalf("stop hooks reran: %#v", calls)
	}
}

func stringsContain(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestAppHooksAfterStartStopsBeforeOrDuringHooks(t *testing.T) {
	t.Run("already stopping", func(t *testing.T) {
		calls := 0
		runner := newApp(appSnapshot{
			afterStart: []HookFunc{func(context.Context) error { calls++; return nil }},
		}, newStaticStopPolicy(time.Second))
		runner.requestStop()
		if err := runner.runAfterStart(context.Background()); !errors.Is(err, errAppStopping) {
			t.Fatalf("runAfterStart = %v, want errAppStopping", err)
		}
		if calls != 0 {
			t.Fatalf("after-start hook ran %d times after stop request", calls)
		}
	})

	t.Run("stop requested by hook", func(t *testing.T) {
		var runner *App
		runner = newApp(appSnapshot{
			afterStart: []HookFunc{func(context.Context) error {
				runner.requestStop()
				return nil
			}},
		}, newStaticStopPolicy(time.Second))
		if err := runner.runAfterStart(context.Background()); !errors.Is(err, errAppStopping) {
			t.Fatalf("runAfterStart = %v, want errAppStopping", err)
		}
	})
}

func TestStartupCallbackErrorPreservesRegistrarTimeoutDuringStop(t *testing.T) {
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.requestStop()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()

	if got := runner.startupCallbackError(ctx, ctx.Err()); got != context.DeadlineExceeded {
		t.Fatalf("startupCallbackError = %v, want context.DeadlineExceeded", got)
	}
}

func TestAppHooksFinalErrorAndFallbackShutdownContext(t *testing.T) {
	finalErr := errors.New("final cleanup failed")
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.setFinalError(func() error { return finalErr })
	if err := runner.runAfterStop(context.Background()); !errors.Is(err, finalErr) {
		t.Fatalf("runAfterStop final error = %v, want %v", err, finalErr)
	}

	type contextKey string
	ctx, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		contextKey("key"),
		"value",
	))
	cancel()
	clean := newApp(appSnapshot{}, newStaticStopPolicy(time.Second)).shutdownContext(ctx)
	if clean.Err() != nil || clean.Value(contextKey("key")) != "value" {
		t.Fatalf("shutdown fallback context = err %v, value %v", clean.Err(), clean.Value(contextKey("key")))
	}
}

func TestAppHooksFreezesStopTimeoutOnFirstRequest(t *testing.T) {
	manager := &stopPolicyConfigManager{initial: validAppConfig(time.Second)}
	policy, cleanup, err := NewStopPolicy(validAppConfig(time.Second), manager, kratoslog.NewStdLogger(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	runner := newApp(appSnapshot{}, policy)
	runner.requestStop()
	manager.publish(validAppConfig(2*time.Second), nil)
	if got := policy.current(); got != 2*time.Second {
		t.Fatalf("updated policy = %s", got)
	}
	if got := runner.stopTimeout(); got != time.Second {
		t.Fatalf("frozen stop timeout = %s, want 1s", got)
	}
}

func TestStartupCallbackPreservesFailureJoinedWithCancellation(t *testing.T) {
	failure := errors.New("register failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := newApp(appSnapshot{}, newStaticStopPolicy(time.Second))
	runner.requestStop()
	cause := errors.Join(context.Canceled, failure)
	if got := runner.startupCallbackError(ctx, cause); !errors.Is(got, failure) {
		t.Fatalf("startup error = %v, want original failure", got)
	}
	if got := runner.startupCallbackError(ctx, context.Canceled); !errors.Is(got, errAppStopping) {
		t.Fatalf("pure cancellation = %v, want normal stopping", got)
	}
}
