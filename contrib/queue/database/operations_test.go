package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

func TestStoreQueueOperationsRequiresOptionalRepo(t *testing.T) {
	if operations, ok := NewStore(repoStub{}).QueueOperations(); ok || operations != nil {
		t.Fatalf("plain Repo exposed operations: %v", operations)
	}
	repo := operationsRepoStub{}
	operations, ok := NewStore(repo).QueueOperations()
	if !ok || operations == nil {
		t.Fatal("OperationsRepo was not exposed")
	}
}

func TestOperationsValidateAndDetachResults(t *testing.T) {
	body := []byte("body")
	wantMutation := errors.New("mutation reached repo")
	repo := operationsRepoStub{
		listTasks: func(context.Context, queue.TaskQuery) (queue.TaskPage, error) {
			return queue.TaskPage{Tasks: []queue.TaskSummary{{ID: "task", Status: queue.TaskStatusFailed}}}, nil
		},
		getTask: func(context.Context, string) (queue.TaskSnapshot, error) {
			return queue.TaskSnapshot{Summary: queue.TaskSummary{ID: "task"}, Task: &queue.Task{ID: "task", Payload: body}}, nil
		},
		deleteTask: func(context.Context, string) error { return wantMutation },
		cancelTask: func(context.Context, string) error { return wantMutation },
		retryTask:  func(context.Context, string, time.Time) error { return wantMutation },
		cleanupTasks: func(context.Context, queue.CleanupOptions) (queue.CleanupResult, error) {
			return queue.CleanupResult{Deleted: 1}, nil
		},
	}
	operations, _ := NewStore(repo).QueueOperations()
	if _, err := operations.List(context.Background(), queue.TaskQuery{}); err == nil {
		t.Fatal("invalid query accepted")
	}
	page, err := operations.List(context.Background(), queue.TaskQuery{Limit: 1})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "task" {
		t.Fatalf("List() = %#v, %v", page, err)
	}
	snapshot, err := operations.Get(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Task.Payload[0] = 'B'
	if string(body) != "body" {
		t.Fatal("Get() returned shared payload")
	}
	if _, err := operations.Get(context.Background(), " "); !errors.Is(err, queue.ErrNotFound) {
		t.Fatalf("invalid id error = %v", err)
	}
	if _, err := operations.Cleanup(context.Background(), queue.CleanupOptions{}); err == nil {
		t.Fatal("invalid cleanup accepted")
	}
	for name, operation := range map[string]func() error{
		"delete": func() error { return operations.Delete(context.Background(), "task") },
		"cancel": func() error { return operations.Cancel(context.Background(), "task") },
		"retry":  func() error { return operations.Retry(context.Background(), "task", time.Now()) },
	} {
		if err := operation(); !errors.Is(err, wantMutation) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	cleanup, err := operations.Cleanup(context.Background(), queue.CleanupOptions{
		Before: time.Now(), Limit: 1, Statuses: []queue.TaskStatus{queue.TaskStatusFailed},
	})
	if err != nil || cleanup.Deleted != 1 {
		t.Fatalf("Cleanup() = %#v, %v", cleanup, err)
	}
}

type operationsRepoStub struct {
	repoStub
	listTasks    func(context.Context, queue.TaskQuery) (queue.TaskPage, error)
	getTask      func(context.Context, string) (queue.TaskSnapshot, error)
	deleteTask   func(context.Context, string) error
	cancelTask   func(context.Context, string) error
	retryTask    func(context.Context, string, time.Time) error
	cleanupTasks func(context.Context, queue.CleanupOptions) (queue.CleanupResult, error)
}

func (r operationsRepoStub) ListTasks(ctx context.Context, query queue.TaskQuery) (queue.TaskPage, error) {
	return r.listTasks(ctx, query)
}
func (r operationsRepoStub) GetTask(ctx context.Context, id string) (queue.TaskSnapshot, error) {
	return r.getTask(ctx, id)
}
func (r operationsRepoStub) DeleteTask(ctx context.Context, id string) error {
	return r.deleteTask(ctx, id)
}
func (r operationsRepoStub) CancelTask(ctx context.Context, id string) error {
	return r.cancelTask(ctx, id)
}
func (r operationsRepoStub) RetryTask(ctx context.Context, id string, at time.Time) error {
	return r.retryTask(ctx, id, at)
}
func (r operationsRepoStub) CleanupTasks(ctx context.Context, options queue.CleanupOptions) (queue.CleanupResult, error) {
	return r.cleanupTasks(ctx, options)
}
