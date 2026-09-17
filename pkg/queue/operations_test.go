package queue

import (
	"context"
	"testing"
	"time"
)

func TestTaskQueryValidateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		query TaskQuery
	}{
		{name: "zero limit", query: TaskQuery{}},
		{name: "limit too large", query: TaskQuery{Limit: 1001}},
		{name: "unknown status", query: TaskQuery{Limit: 1, Statuses: []TaskStatus{"unknown"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.query.Validate(); err == nil {
				t.Fatalf("Validate() error = nil for %#v", test.query)
			}
		})
	}
}

func TestTaskSnapshotCloneDetachesTaskBody(t *testing.T) {
	original := TaskSnapshot{Summary: TaskSummary{ID: "task"}, Task: &Task{ID: "task", Payload: []byte("body"), Headers: map[string]string{"secret": "value"}}}
	clone := original.Clone()
	clone.Task.Payload[0] = 'B'
	clone.Task.Headers["secret"] = "changed"
	if string(original.Task.Payload) != "body" || original.Task.Headers["secret"] != "value" {
		t.Fatalf("Clone() shares task body: %#v", original.Task)
	}
}

func TestCleanupOptionsValidateRequiresBoundedTerminalStates(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		options CleanupOptions
		wantErr bool
	}{
		{name: "failed", options: CleanupOptions{Before: now, Limit: 1, Statuses: []TaskStatus{TaskStatusFailed}}},
		{name: "completed", options: CleanupOptions{Before: now, Limit: 1000, Statuses: []TaskStatus{TaskStatusCompleted}}},
		{name: "missing cutoff", options: CleanupOptions{Limit: 1, Statuses: []TaskStatus{TaskStatusFailed}}, wantErr: true},
		{name: "running", options: CleanupOptions{Before: now, Limit: 1, Statuses: []TaskStatus{TaskStatusRunning}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.options.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestQueueOperationsDiscoversOptionalCapabilities(t *testing.T) {
	operations := operationStub{}
	tests := []struct {
		name  string
		store Store
		want  bool
	}{
		{name: "direct operations", store: operationStore{}, want: true},
		{name: "dynamic provider", store: providerStore{operations: operations}, want: true},
		{name: "unsupported provider", store: providerStore{}, want: false},
		{name: "plain store", store: operationsStoreStub{}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := &Queue[string]{store: test.store}
			got, ok := queue.Operations()
			if ok != test.want || (ok && got == nil) {
				t.Fatalf("Operations() = (%v, %t), want supported=%t", got, ok, test.want)
			}
		})
	}
}

type operationStub struct{}

func (operationStub) List(context.Context, TaskQuery) (TaskPage, error) { return TaskPage{}, nil }
func (operationStub) Get(context.Context, string) (TaskSnapshot, error) { return TaskSnapshot{}, nil }
func (operationStub) Delete(context.Context, string) error              { return nil }
func (operationStub) Cancel(context.Context, string) error              { return nil }
func (operationStub) Retry(context.Context, string, time.Time) error    { return nil }
func (operationStub) Cleanup(context.Context, CleanupOptions) (CleanupResult, error) {
	return CleanupResult{}, nil
}

type operationStore struct {
	operationsStoreStub
}

func (operationStore) List(context.Context, TaskQuery) (TaskPage, error) { return TaskPage{}, nil }
func (operationStore) Get(context.Context, string) (TaskSnapshot, error) { return TaskSnapshot{}, nil }
func (operationStore) Delete(context.Context, string) error              { return nil }
func (operationStore) Cancel(context.Context, string) error              { return nil }
func (operationStore) Cleanup(context.Context, CleanupOptions) (CleanupResult, error) {
	return CleanupResult{}, nil
}

type providerStore struct {
	operationsStoreStub
	operations Operations
}

func (s providerStore) QueueOperations() (Operations, bool) {
	return s.operations, s.operations != nil
}

type operationsStoreStub struct{}

func (operationsStoreStub) Enqueue(context.Context, *Task) error { return nil }
func (operationsStoreStub) Reserve(context.Context, time.Time, time.Duration) (*Reservation, error) {
	return nil, nil
}
func (operationsStoreStub) Ack(context.Context, *Reservation) error                { return nil }
func (operationsStoreStub) Release(context.Context, *Reservation, time.Time) error { return nil }
func (operationsStoreStub) Fail(context.Context, *Reservation, string, time.Time) error {
	return nil
}
func (operationsStoreStub) Failed(context.Context, int) ([]FailedTask, error) { return nil, nil }
func (operationsStoreStub) Retry(context.Context, string, time.Time) error    { return nil }
