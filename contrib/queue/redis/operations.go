package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

const (
	maxOperationsCursorBytes = 1 << 20
	maxOperationsPendingIDs  = 4000
)

type operationsCursor struct {
	Scan    uint64   `json:"scan"`
	Pending []string `json:"pending,omitempty"`
	Done    bool     `json:"done,omitempty"`
}

type inspectedTask struct {
	id       string
	raw      *goredis.StringCmd
	delayed  *goredis.FloatCmd
	reserved *goredis.FloatCmd
	failed   *goredis.FloatCmd
}

// List 使用 Redis HSCAN 游标分批读取元数据；响应不包含任务正文。
func (s *Store) List(ctx context.Context, query queue.TaskQuery) (queue.TaskPage, error) {
	if err := query.Validate(); err != nil {
		return queue.TaskPage{}, err
	}
	if onlyCompleted(query.Statuses) {
		return queue.TaskPage{}, nil
	}
	state, err := decodeCursor(query.Cursor)
	if err != nil {
		return queue.TaskPage{}, err
	}
	budget := max(64, query.Limit*4)
	budget = min(budget, 4000)
	page := queue.TaskPage{Tasks: make([]queue.TaskSummary, 0, query.Limit)}
	for len(page.Tasks) < query.Limit && budget > 0 {
		if len(state.Pending) == 0 {
			if state.Done {
				break
			}
			values, next, scanErr := s.client.HScan(ctx, s.keys[0], state.Scan, "*", int64(min(budget, max(64, query.Limit)))).Result()
			if scanErr != nil {
				return queue.TaskPage{}, fmt.Errorf("scan redis queue tasks: %w", scanErr)
			}
			state.Scan = next
			state.Done = next == 0
			for index := 0; index+1 < len(values); index += 2 {
				state.Pending = append(state.Pending, values[index])
			}
			if len(state.Pending) == 0 {
				continue
			}
		}
		count := min(len(state.Pending), budget)
		batchIDs := state.Pending[:count]
		items, inspectErr := s.inspect(ctx, batchIDs)
		if inspectErr != nil {
			return queue.TaskPage{}, inspectErr
		}
		state.Pending = state.Pending[count:]
		budget -= count
		for index, item := range items {
			if item == nil || !matchesStatus(query.Statuses, item.Summary.Status) {
				continue
			}
			page.Tasks = append(page.Tasks, item.Summary)
			if len(page.Tasks) == query.Limit {
				// HSCAN 一批可能返回多条符合条件的记录，未消费 ID 必须随游标返回，避免翻页丢任务。
				state.Pending = append(append([]string(nil), batchIDs[index+1:]...), state.Pending...)
				break
			}
		}
	}
	if len(state.Pending) > 0 || !state.Done {
		page.NextCursor, err = encodeCursor(state)
		if err != nil {
			return queue.TaskPage{}, err
		}
	}
	return page, nil
}

// Get 返回指定任务的完整独立快照。
func (s *Store) Get(ctx context.Context, id string) (queue.TaskSnapshot, error) {
	if !validOperationsID(id) {
		return queue.TaskSnapshot{}, queue.ErrNotFound
	}
	items, err := s.inspect(ctx, []string{id})
	if err != nil {
		return queue.TaskSnapshot{}, err
	}
	if len(items) == 0 || items[0] == nil {
		return queue.TaskSnapshot{}, queue.ErrNotFound
	}
	return items[0].Clone(), nil
}

// Delete 仅删除失败任务；Redis Store 不保留完成记录。
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.mutate(ctx, "delete", id, time.Time{})
}

// Cancel 仅删除 pending 或 scheduled 任务，有效租约任务返回 ErrStateConflict。
func (s *Store) Cancel(ctx context.Context, id string) error {
	return s.mutate(ctx, "cancel", id, time.Time{})
}

// Cleanup 有界删除截止时间之前的失败任务；Redis Store 没有 completed 记录。
func (s *Store) Cleanup(ctx context.Context, options queue.CleanupOptions) (queue.CleanupResult, error) {
	if err := options.Validate(); err != nil {
		return queue.CleanupResult{}, err
	}
	if !containsStatus(options.Statuses, queue.TaskStatusFailed) {
		return queue.CleanupResult{}, nil
	}
	deleted, err := s.client.Eval(ctx, operationsCleanupScript, s.keys, scheduledMillis(options.Before), options.Limit).Int()
	if err != nil {
		return queue.CleanupResult{}, fmt.Errorf("cleanup redis queue tasks: %w", err)
	}
	return queue.CleanupResult{Deleted: deleted}, nil
}

func (s *Store) mutate(ctx context.Context, action, id string, at time.Time) error {
	if !validOperationsID(id) {
		return queue.ErrNotFound
	}
	result, err := s.client.Eval(ctx, operationsMutationScript, s.keys, id, action, time.Now().UTC().UnixMilli(), scheduledMillis(at), at.Format(time.RFC3339Nano)).Int()
	if err != nil {
		return fmt.Errorf("%s redis queue task: %w", action, err)
	}
	switch result {
	case 1:
		return nil
	case -1:
		return queue.ErrStateConflict
	default:
		return queue.ErrNotFound
	}
}

func (s *Store) inspect(ctx context.Context, ids []string) ([]*queue.TaskSnapshot, error) {
	commands := make([]inspectedTask, len(ids))
	_, err := s.client.Pipelined(ctx, func(pipe goredis.Pipeliner) error {
		for index, id := range ids {
			commands[index] = inspectedTask{
				id: id, raw: pipe.HGet(ctx, s.keys[0], id), delayed: pipe.ZScore(ctx, s.keys[2], id),
				reserved: pipe.ZScore(ctx, s.keys[3], id), failed: pipe.ZScore(ctx, s.keys[4], id),
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("inspect redis queue tasks: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	result := make([]*queue.TaskSnapshot, len(commands))
	for index, command := range commands {
		raw, rawErr := command.raw.Result()
		if errors.Is(rawErr, goredis.Nil) {
			continue
		}
		if rawErr != nil {
			return nil, fmt.Errorf("read redis queue task %q: %w", command.id, rawErr)
		}
		var value record
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("decode redis queue record %q: %w", command.id, err)
		}
		task := &queue.Task{}
		if err := json.Unmarshal([]byte(value.Task), task); err != nil || task.ID != command.id || task.MessageVersion == "" {
			return nil, fmt.Errorf("decode redis queue task %q: invalid stored payload", command.id)
		}
		status := queue.TaskStatusPending
		reservedUntil := scoreTime(command.reserved)
		failedAt := time.Time{}
		if _, scoreErr := command.failed.Result(); scoreErr == nil {
			status = queue.TaskStatusFailed
			failedAt = time.UnixMilli(value.FailedAt).UTC()
		} else if !errors.Is(scoreErr, goredis.Nil) {
			return nil, fmt.Errorf("read redis failed state %q: %w", command.id, scoreErr)
		} else if score, scoreErr := command.reserved.Result(); scoreErr == nil && int64(score) > now {
			status = queue.TaskStatusRunning
		} else if scoreErr != nil && !errors.Is(scoreErr, goredis.Nil) {
			return nil, fmt.Errorf("read redis reserved state %q: %w", command.id, scoreErr)
		} else if score, scoreErr := command.delayed.Result(); scoreErr == nil && int64(score) > now {
			status = queue.TaskStatusScheduled
		} else if scoreErr != nil && !errors.Is(scoreErr, goredis.Nil) {
			return nil, fmt.Errorf("read redis delayed state %q: %w", command.id, scoreErr)
		}
		summary := queue.TaskSummary{ID: task.ID, MessageVersion: task.MessageVersion, Status: status, Attempts: value.Attempts, AvailableAt: task.AvailableAt, CreatedAt: task.CreatedAt, ReservedUntil: reservedUntil, FailedAt: failedAt, FailureReason: value.Reason}
		result[index] = &queue.TaskSnapshot{Summary: summary, Task: task}
	}
	return result, nil
}

func scoreTime(command *goredis.FloatCmd) time.Time {
	score, err := command.Result()
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(int64(score)).UTC()
}

func matchesStatus(filter []queue.TaskStatus, status queue.TaskStatus) bool {
	return len(filter) == 0 || containsStatus(filter, status)
}

func containsStatus(statuses []queue.TaskStatus, target queue.TaskStatus) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}

func onlyCompleted(statuses []queue.TaskStatus) bool {
	return len(statuses) > 0 && !containsStatus(statuses, queue.TaskStatusPending) && !containsStatus(statuses, queue.TaskStatusScheduled) && !containsStatus(statuses, queue.TaskStatusRunning) && !containsStatus(statuses, queue.TaskStatusFailed)
}

func validOperationsID(id string) bool {
	return len(id) <= 128 && strings.TrimSpace(id) != ""
}

func encodeCursor(cursor operationsCursor) (string, error) {
	if err := validateCursor(cursor); err != nil {
		return "", err
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode redis queue cursor: %w", err)
	}
	if len(data) > maxOperationsCursorBytes {
		return "", errors.New("redis queue cursor is too large")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(cursor string) (operationsCursor, error) {
	if cursor == "" {
		return operationsCursor{}, nil
	}
	if len(cursor) > base64.RawURLEncoding.EncodedLen(maxOperationsCursorBytes) {
		return operationsCursor{}, errors.New("invalid redis queue cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(data) > maxOperationsCursorBytes {
		return operationsCursor{}, errors.New("invalid redis queue cursor")
	}
	var result operationsCursor
	if err := json.Unmarshal(data, &result); err != nil {
		return operationsCursor{}, errors.New("invalid redis queue cursor")
	}
	if err := validateCursor(result); err != nil {
		return operationsCursor{}, errors.New("invalid redis queue cursor")
	}
	return result, nil
}

func validateCursor(cursor operationsCursor) error {
	if len(cursor.Pending) > maxOperationsPendingIDs {
		return errors.New("redis queue cursor has too many pending task IDs")
	}
	for _, id := range cursor.Pending {
		if !validOperationsID(id) {
			return errors.New("redis queue cursor contains an invalid task ID")
		}
	}
	return nil
}

var _ queue.Operations = (*Store)(nil)
