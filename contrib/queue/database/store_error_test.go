package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// errorRepoStub 在 repoStub 之上实现 ErrorRepo，记录 Store 转交的执行错误。
type errorRepoStub struct {
	repoStub
	calls []string
	errs  []queue.ExecutionError
}

func (r *errorRepoStub) ReleaseReservedWithError(_ context.Context, id, token string, _ time.Time, executionError queue.ExecutionError) error {
	r.calls = append(r.calls, "release:"+id+":"+token)
	r.errs = append(r.errs, executionError)
	return nil
}

func (r *errorRepoStub) FailReservedWithError(_ context.Context, id, token, reason string, _ time.Time, executionError queue.ExecutionError) error {
	r.calls = append(r.calls, "fail:"+id+":"+token+":"+reason)
	r.errs = append(r.errs, executionError)
	return nil
}

func TestStoreRecordsExecutionErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	reservation := &queue.Reservation{Task: &queue.Task{ID: "a"}, Token: "t", Attempts: 1}
	executionError := queue.ExecutionError{Message: "boom", At: now}

	repo := &errorRepoStub{}
	store := NewStore(repo)
	if err := store.ReleaseWithError(ctx, reservation, now, executionError); err != nil {
		t.Fatal(err)
	}
	if err := store.FailWithError(ctx, reservation, "permanent", now, executionError); err != nil {
		t.Fatal(err)
	}
	if strings.Join(repo.calls, ",") != "release:a:t,fail:a:t:permanent" || repo.errs[0] != executionError || repo.errs[1] != executionError {
		t.Fatalf("calls=%v errs=%v", repo.calls, repo.errs)
	}

	for name, invalid := range map[string]queue.ExecutionError{
		"too long":     {Message: strings.Repeat("x", queue.MaxErrorMessageBytes+1), At: now},
		"invalid utf8": {Message: "\xff", At: now},
		"zero time":    {Message: "boom"},
	} {
		if err := store.ReleaseWithError(ctx, reservation, now, invalid); err == nil {
			t.Fatalf("%s accepted by release", name)
		}
		if err := store.FailWithError(ctx, reservation, "permanent", now, invalid); err == nil {
			t.Fatalf("%s accepted by fail", name)
		}
	}
	if err := store.FailWithError(ctx, reservation, strings.Repeat("r", 129), now, executionError); err == nil {
		t.Fatal("long reason accepted")
	}
	for _, invalid := range []*queue.Reservation{nil, {Task: &queue.Task{ID: "a"}}} {
		if err := store.ReleaseWithError(ctx, invalid, now, executionError); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("invalid reservation release = %v", err)
		}
		if err := store.FailWithError(ctx, invalid, "permanent", now, executionError); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("invalid reservation fail = %v", err)
		}
	}
	if len(repo.calls) != 2 {
		t.Fatalf("invalid input reached repo: %v", repo.calls)
	}
}

func TestStoreErrorRecordingFallsBackWithoutErrorRepo(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls []string
	store := NewStore(repoStub{
		release: func(context.Context, string, string, time.Time) error { calls = append(calls, "release"); return nil },
		fail: func(context.Context, string, string, string, time.Time) error {
			calls = append(calls, "fail")
			return nil
		},
	})
	reservation := &queue.Reservation{Task: &queue.Task{ID: "a"}, Token: "t", Attempts: 1}
	executionError := queue.ExecutionError{Message: "boom", At: now}
	if err := store.ReleaseWithError(ctx, reservation, now, executionError); err != nil {
		t.Fatal(err)
	}
	if err := store.FailWithError(ctx, reservation, "permanent", now, executionError); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "release,fail" {
		t.Fatalf("fallback calls %v", calls)
	}
}

func TestStoreFailedReturnsLastError(t *testing.T) {
	at := time.Unix(100, 0).UTC()
	store := NewStore(repoStub{list: func(context.Context, int) ([]TaskRecord, error) {
		return []TaskRecord{{Task: queue.Task{ID: "a", MessageVersion: "v"}, Failed: true, FailureReason: "permanent", FailedAt: at, LastError: "boom", LastErrorAt: at}}, nil
	}})
	failed, err := store.Failed(context.Background(), 10)
	if err != nil || len(failed) != 1 || failed[0].LastError != "boom" || !failed[0].LastErrorAt.Equal(at) {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}
