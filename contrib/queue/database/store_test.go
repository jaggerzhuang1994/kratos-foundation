package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

type repoStub struct {
	Repo
	insert   func(context.Context, *TaskRecord) error
	claim    func(context.Context, time.Time, time.Time, string) (*TaskRecord, error)
	complete func(context.Context, string, string, time.Time) error
	release  func(context.Context, string, string, time.Time) error
	fail     func(context.Context, string, string, string, time.Time) error
	list     func(context.Context, int) ([]TaskRecord, error)
	retry    func(context.Context, string, time.Time) error
}

func (r repoStub) Insert(c context.Context, m *TaskRecord) error { return r.insert(c, m) }
func (r repoStub) Claim(c context.Context, n, u time.Time, t string) (*TaskRecord, error) {
	return r.claim(c, n, u, t)
}
func (r repoStub) CompleteReserved(c context.Context, i, t string, at time.Time) error {
	return r.complete(c, i, t, at)
}
func (r repoStub) ReleaseReserved(c context.Context, i, t string, a time.Time) error {
	return r.release(c, i, t, a)
}
func (r repoStub) FailReserved(c context.Context, i, t, reason string, a time.Time) error {
	return r.fail(c, i, t, reason, a)
}
func (r repoStub) ListFailed(c context.Context, l int) ([]TaskRecord, error) { return r.list(c, l) }
func (r repoStub) RetryFailed(c context.Context, i string, a time.Time) error {
	return r.retry(c, i, a)
}

func TestStoreEnqueueSnapshot(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(10, 100)
	task := &queue.Task{ID: "A ", Type: "email", Payload: []byte("body"), Headers: map[string]string{"key": "value"}, AvailableAt: now}
	var saved *TaskRecord
	s := NewStore(repoStub{insert: func(c context.Context, r *TaskRecord) error {
		if c != ctx {
			t.Fatal("context lost")
		}
		saved = r
		return queue.ErrDuplicate
	}})
	if err := s.Enqueue(ctx, task); !errors.Is(err, queue.ErrDuplicate) {
		t.Fatal(err)
	}
	if saved.Task.ID != "A " || !saved.Task.AvailableAt.Equal(now) || saved.Attempts != 0 || saved.Token != "" || !saved.ReservedUntil.IsZero() || saved.Failed {
		t.Fatalf("record %#v", saved)
	}
	saved.Task.Payload[0] = 'X'
	saved.Task.Headers["key"] = "changed"
	if string(task.Payload) != "body" || task.Headers["key"] != "value" {
		t.Fatal("shared task")
	}
	for _, input := range []*queue.Task{nil, {}, {ID: " ", Type: "x"}, {ID: strings.Repeat("x", 129), Type: "x"}, {ID: "a", Type: " "}} {
		if s.Enqueue(ctx, input) == nil {
			t.Fatal("invalid task accepted")
		}
	}
}

func TestStoreClaimSnapshotAndValidation(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(10, 100)
	until := now.Add(time.Second)
	for _, tc := range []struct {
		name   string
		change func(*TaskRecord)
		valid  bool
	}{
		{"valid", func(*TaskRecord) {}, true},
		{"rounded expiry", func(r *TaskRecord) { r.ReservedUntil = r.ReservedUntil.Add(time.Millisecond) }, true},
		{"wrong token", func(r *TaskRecord) { r.Token = "other" }, false},
		{"early expiry", func(r *TaskRecord) { r.ReservedUntil = r.ReservedUntil.Add(-time.Nanosecond) }, false},
		{"no attempts", func(r *TaskRecord) { r.Attempts = 0 }, false},
		{"future task", func(r *TaskRecord) { r.Task.AvailableAt = now.Add(time.Second) }, false},
		{"failed", func(r *TaskRecord) { r.Failed = true }, false},
		{"invalid task", func(r *TaskRecord) { r.Task.Type = " " }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saved *TaskRecord
			s := NewStore(repoStub{claim: func(c context.Context, n, u time.Time, token string) (*TaskRecord, error) {
				if c != ctx || !n.Equal(now) || !u.Equal(until) || token == "" {
					t.Fatal("claim args")
				}
				saved = &TaskRecord{Task: queue.Task{ID: "one", Type: "email", Payload: []byte("body")}, Token: token, ReservedUntil: u, Attempts: 2}
				tc.change(saved)
				return saved, nil
			}})
			got, err := s.Reserve(ctx, now, time.Second)
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid claim accepted")
				}
				return
			}
			if err != nil || got.Attempts != 2 {
				t.Fatalf("claim %#v %v", got, err)
			}
			got.Task.Payload[0] = 'X'
			if string(saved.Task.Payload) != "body" {
				t.Fatal("shared snapshot")
			}
		})
	}
	for _, failure := range []error{nil, context.Canceled, errors.New("offline")} {
		s := NewStore(repoStub{claim: func(context.Context, time.Time, time.Time, string) (*TaskRecord, error) { return nil, failure }})
		got, err := s.Reserve(ctx, now, time.Second)
		if got != nil || !errors.Is(err, failure) {
			t.Fatalf("lost result %v %v", got, err)
		}
	}
	if _, err := NewStore(nil).Reserve(ctx, now, 0); err == nil {
		t.Fatal("invalid lease")
	}
}

func TestStoreOwnedTransitions(t *testing.T) {
	ctx := context.Background()
	at := time.Unix(10, 100)
	check := func(c context.Context, id, token string) {
		t.Helper()
		if c != ctx || id != "one" || token != "lease" {
			t.Fatal("lost ownership/context")
		}
	}
	s := NewStore(repoStub{
		complete: func(c context.Context, id, token string, completedAt time.Time) error {
			check(c, id, token)
			if completedAt.IsZero() {
				t.Error("missing completion time")
			}
			return queue.ErrLeaseLost
		},
		release: func(c context.Context, id, token string, n time.Time) error {
			check(c, id, token)
			if !n.Equal(at) {
				t.Fatal("time changed")
			}
			return queue.ErrLeaseLost
		},
		fail: func(c context.Context, id, token, reason string, n time.Time) error {
			check(c, id, token)
			if reason != "permanent" || !n.Equal(at) {
				t.Fatal("failure changed")
			}
			return queue.ErrLeaseLost
		},
		retry: func(c context.Context, id string, n time.Time) error {
			if c != ctx || id != "one" || !n.Equal(at) {
				t.Fatal("retry changed")
			}
			return queue.ErrNotFound
		},
	})
	r := &queue.Reservation{Task: &queue.Task{ID: "one"}, Token: "lease"}
	for _, err := range []error{s.Ack(ctx, r), s.Release(ctx, r, at), s.Fail(ctx, r, "permanent", at)} {
		if !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("lost error %v", err)
		}
	}
	if !errors.Is(s.Retry(ctx, "one", at), queue.ErrNotFound) {
		t.Fatal("lost not found")
	}
	for _, r := range []*queue.Reservation{nil, {}, {Task: &queue.Task{ID: "one"}}} {
		for _, err := range []error{s.Ack(ctx, r), s.Release(ctx, r, at), s.Fail(ctx, r, "permanent", at)} {
			if !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("invalid ownership")
			}
		}
	}
	if s.Fail(ctx, r, strings.Repeat("x", 129), at) == nil {
		t.Fatal("long reason")
	}
	if !errors.Is(s.Retry(ctx, " ", at), queue.ErrNotFound) {
		t.Fatal("empty retry ID")
	}
}

func TestStoreFailedSnapshots(t *testing.T) {
	ctx := context.Background()
	record := TaskRecord{Task: queue.Task{ID: "one", Type: "x", Payload: []byte("body")}, Failed: true, Attempts: 3, FailureReason: "permanent", FailedAt: time.Unix(20, 100)}
	s := NewStore(repoStub{list: func(c context.Context, n int) ([]TaskRecord, error) {
		if c != ctx || n != 1 {
			t.Fatal("list args")
		}
		return []TaskRecord{record}, nil
	}})
	results, err := s.Failed(ctx, 1)
	if err != nil || len(results) != 1 || results[0].Attempts != 3 || results[0].Reason != "permanent" || !results[0].FailedAt.Equal(record.FailedAt) {
		t.Fatalf("list %#v %v", results, err)
	}
	results[0].Task.Payload[0] = 'X'
	if string(record.Task.Payload) != "body" {
		t.Fatal("shared data")
	}
	for _, limit := range []int{0, 1001} {
		if _, err := s.Failed(ctx, limit); err == nil {
			t.Fatal("invalid limit")
		}
	}
	failure := errors.New("read failed")
	for _, tc := range []struct {
		name    string
		records []TaskRecord
		err     error
	}{
		{"storage", nil, failure}, {"too many", []TaskRecord{record, record}, nil}, {"wrong state", []TaskRecord{{}}, nil}, {"invalid task", []TaskRecord{{Failed: true}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(repoStub{list: func(context.Context, int) ([]TaskRecord, error) { return tc.records, tc.err }})
			_, err := s.Failed(ctx, 1)
			if err == nil {
				t.Fatal("invalid result")
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("lost failure")
			}
		})
	}
}

func TestStoresUseIndependentRepos(t *testing.T) {
	var first, second []string
	newRepo := func(ids *[]string) Repo {
		return repoStub{insert: func(_ context.Context, r *TaskRecord) error { *ids = append(*ids, r.Task.ID); return nil }}
	}
	email, report := NewStore(newRepo(&first)), NewStore(newRepo(&second))
	if err := email.Enqueue(context.Background(), &queue.Task{ID: "same-id", Type: "email"}); err != nil {
		t.Fatal(err)
	}
	if err := report.Enqueue(context.Background(), &queue.Task{ID: "same-id", Type: "report"}); err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("repo routing was shared")
	}
}
