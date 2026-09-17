package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

func TestOperationsListMetadataAndGetBody(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	task := &queue.Task{ID: "task", MessageVersion: "job.v1", Payload: []byte("body"), Headers: map[string]string{"secret": "value"}, AvailableAt: now.Add(time.Hour), CreatedAt: now}
	taskJSON, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	recordJSON, err := json.Marshal(record{Task: string(taskJSON), Attempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: "unused"})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(operationsHook{record: string(recordJSON), delayed: float64(now.Add(time.Hour).UnixMilli())})
	store := &Store{client: client, keys: []string{"q:tasks", "q:ready", "q:delayed", "q:reserved", "q:failed"}}
	page, err := store.List(context.Background(), queue.TaskQuery{Limit: 1})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "task" || page.Tasks[0].Status != queue.TaskStatusScheduled {
		t.Fatalf("List() = %#v, %v", page, err)
	}
	detail, err := store.Get(context.Background(), "task")
	if err != nil || detail.Task == nil || string(detail.Task.Payload) != "body" {
		t.Fatalf("Get() = %#v, %v", detail, err)
	}
	detail.Task.Payload[0] = 'B'
	if string(task.Payload) != "body" {
		t.Fatal("Get() shared caller payload")
	}
}

func TestOperationsListCursorPreservesUnconsumedScanBatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	records := make(map[string]string, 2)
	for _, id := range []string{"first", "second"} {
		taskJSON, err := json.Marshal(&queue.Task{ID: id, MessageVersion: "job.v1", CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		recordJSON, err := json.Marshal(record{Task: string(taskJSON)})
		if err != nil {
			t.Fatal(err)
		}
		records[id] = string(recordJSON)
	}
	client := goredis.NewClient(&goredis.Options{Addr: "unused"})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(operationsPageHook{records: records})
	store := &Store{client: client, keys: []string{"q:tasks", "q:ready", "q:delayed", "q:reserved", "q:failed"}}

	first, err := store.List(context.Background(), queue.TaskQuery{Limit: 1})
	if err != nil || len(first.Tasks) != 1 || first.NextCursor == "" {
		t.Fatalf("first List() = %#v, %v", first, err)
	}
	second, err := store.List(context.Background(), queue.TaskQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Tasks) != 1 || second.NextCursor != "" {
		t.Fatalf("second List() = %#v, %v", second, err)
	}
	if first.Tasks[0].ID == second.Tasks[0].ID {
		t.Fatalf("pagination repeated %q instead of preserving the second task", first.Tasks[0].ID)
	}
}

func TestDecodeCursorRejectsUnboundedOrInvalidPendingIDs(t *testing.T) {
	tests := []operationsCursor{
		{Pending: make([]string, 4001)},
		{Pending: []string{strings.Repeat("x", 129)}},
		{Pending: []string{" "}},
	}
	for index, state := range tests {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		cursor := base64.RawURLEncoding.EncodeToString(data)
		if _, err := decodeCursor(cursor); err == nil {
			t.Fatalf("case %d accepted invalid cursor", index)
		}
	}
}

func TestOperationsMutationsMapAtomicResults(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "unused"})
	t.Cleanup(func() { _ = client.Close() })
	hook := &operationResultHook{result: 1}
	client.AddHook(hook)
	store := &Store{client: client, keys: []string{"q:tasks", "q:ready", "q:delayed", "q:reserved", "q:failed"}}
	ctx := context.Background()
	for _, operation := range []func() error{
		func() error { return store.Delete(ctx, "task") },
		func() error { return store.Cancel(ctx, "task") },
		func() error { return store.Retry(ctx, "task", time.Now()) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
		hook.result = -1
		if err := operation(); !errors.Is(err, queue.ErrStateConflict) {
			t.Fatalf("conflict error = %v", err)
		}
		hook.result = 0
		if err := operation(); !errors.Is(err, queue.ErrNotFound) {
			t.Fatalf("not found error = %v", err)
		}
		hook.result = 1
	}
	cutoff := time.Unix(1700000000, int64(time.Millisecond/2)).UTC()
	cleanup, err := store.Cleanup(ctx, queue.CleanupOptions{Before: cutoff, Limit: 1, Statuses: []queue.TaskStatus{queue.TaskStatusFailed}})
	if err != nil || cleanup.Deleted != 1 {
		t.Fatalf("Cleanup() = %#v, %v", cleanup, err)
	}
	if got := hook.args[len(hook.args)-2].(int64); got != scheduledMillis(cutoff) {
		t.Fatalf("cleanup cutoff = %d, want %d", got, scheduledMillis(cutoff))
	}
}

type operationsHook struct {
	record  string
	delayed float64
}

func (h operationsHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h operationsHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		return h.set(cmd)
	}
}
func (h operationsHook) ProcessPipelineHook(goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(_ context.Context, commands []goredis.Cmder) error {
		for _, command := range commands {
			if err := h.set(command); err != nil && !errors.Is(err, goredis.Nil) {
				return err
			}
		}
		return nil
	}
}
func (h operationsHook) set(command goredis.Cmder) error {
	switch cmd := command.(type) {
	case *goredis.ScanCmd:
		cmd.SetVal([]string{"task", h.record}, 0)
	case *goredis.StringCmd:
		cmd.SetVal(h.record)
	case *goredis.FloatCmd:
		key := command.Args()[1].(string)
		if strings.HasSuffix(key, ":delayed") {
			cmd.SetVal(h.delayed)
			return nil
		}
		cmd.SetErr(goredis.Nil)
		return goredis.Nil
	}
	return nil
}

type operationResultHook struct {
	result int64
	args   []any
}

type operationsPageHook struct{ records map[string]string }

func (h operationsPageHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h operationsPageHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, command goredis.Cmder) error { return h.set(command) }
}
func (h operationsPageHook) ProcessPipelineHook(goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(_ context.Context, commands []goredis.Cmder) error {
		for _, command := range commands {
			if err := h.set(command); err != nil && !errors.Is(err, goredis.Nil) {
				return err
			}
		}
		return nil
	}
}
func (h operationsPageHook) set(command goredis.Cmder) error {
	switch cmd := command.(type) {
	case *goredis.ScanCmd:
		cmd.SetVal([]string{"first", h.records["first"], "second", h.records["second"]}, 0)
	case *goredis.StringCmd:
		id := command.Args()[2].(string)
		cmd.SetVal(h.records[id])
	case *goredis.FloatCmd:
		cmd.SetErr(goredis.Nil)
		return goredis.Nil
	}
	return nil
}

func (h *operationResultHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *operationResultHook) ProcessHook(goredis.ProcessHook) goredis.ProcessHook {
	return func(_ context.Context, command goredis.Cmder) error {
		h.args = append([]any(nil), command.Args()...)
		command.(*goredis.Cmd).SetVal(h.result)
		return nil
	}
}
func (h *operationResultHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}
