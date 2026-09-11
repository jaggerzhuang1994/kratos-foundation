package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	goredis "github.com/redis/go-redis/v9"
)

// Config 选择具名连接和业务提供的队列键前缀；两项均必填。
type Config struct {
	Connection string
	// KeyPrefix 原样保留，内部追加 :tasks/:ready/:delayed/:reserved/:failed。
	// 同一 Redis 中相同前缀共享一个队列，独立队列必须使用不同前缀。
	KeyPrefix string
}

// Store 借用 Manager 的 client；释放连接由 Manager 的 cleanup 负责。
type Store struct {
	client *goredis.Client
	keys   []string
}

var _ queue.Store = (*Store)(nil)

// NewStore 解析配置与连接，不发送 Redis 命令，也不创建后台 goroutine。
func NewStore(manager foundationredis.Manager, config Config) (*Store, error) {
	config.Connection = strings.TrimSpace(config.Connection)
	if config.Connection == "" || strings.TrimSpace(config.KeyPrefix) == "" {
		return nil, errors.New("redis queue connection and key prefix are required")
	}
	client, err := manager.Connection(config.Connection)
	if err != nil {
		return nil, fmt.Errorf("resolve queue redis connection: %w", err)
	}
	// 前缀由业务管理；不散列或规范化，避免改变业务指定的 Redis 命名空间。
	prefix := config.KeyPrefix + ":"
	return &Store{client: client, keys: []string{prefix + "tasks", prefix + "ready", prefix + "delayed", prefix + "reserved", prefix + "failed"}}, nil
}

type record struct {
	Task     string `json:"task"`
	Attempts int    `json:"attempts"`
	Token    string `json:"token"`
	Reason   string `json:"reason"`
	FailedAt int64  `json:"failed_at"`
}

// Enqueue 保存任务副本，重复 ID 不覆盖待执行或失败记录。
func (s *Store) Enqueue(ctx context.Context, task *queue.Task) error {
	if task == nil || strings.TrimSpace(task.ID) == "" || len(task.ID) > 128 || strings.TrimSpace(task.Type) == "" {
		return errors.New("redis queue task id and type are required")
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("encode queue task: %w", err)
	}
	data, err := json.Marshal(record{Task: string(raw)})
	if err != nil {
		return fmt.Errorf("encode queue record: %w", err)
	}
	result, err := s.client.Eval(ctx, enqueueScript, s.keys, task.ID, string(data), scheduledMillis(task.AvailableAt)).Int()
	if err != nil {
		return fmt.Errorf("enqueue redis task: %w", err)
	}
	if result == 0 {
		return queue.ErrDuplicate
	}
	return nil
}

// Reserve 原子迁移至多 100 个到期任务与 100 个过期租约，再领取一项。
func (s *Store) Reserve(ctx context.Context, now time.Time, lease time.Duration) (*queue.Reservation, error) {
	if lease < time.Millisecond {
		return nil, errors.New("redis queue lease must be at least one millisecond")
	}
	raw, err := s.client.Eval(ctx, reserveScript, s.keys, now.UnixMilli(), scheduledMillis(now.Add(lease)), uuid.NewString()).Text()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reserve redis task: %w", err)
	}
	var result struct {
		ID     string `json:"id"`
		Record record `json:"record"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode redis reservation: %w", err)
	}
	task := &queue.Task{}
	reservation := &queue.Reservation{Task: task, Token: result.Record.Token, Attempts: result.Record.Attempts}
	if err := json.Unmarshal([]byte(result.Record.Task), task); err != nil || task.ID != result.ID || task.Type == "" {
		// 已取得租约的数据损坏时转入失败集合，保留原始数据供排障，避免不断领取。
		task.ID = result.ID
		failErr := s.Fail(ctx, reservation, "corrupt_payload", now)
		return nil, errors.Join(errors.New("decode redis task: invalid stored payload"), failErr)
	}
	return reservation, nil
}

// Ack 仅删除 token 仍匹配的当前领取任务。
func (s *Store) Ack(ctx context.Context, reservation *queue.Reservation) error {
	return s.transition(ctx, reservation, "ack", time.Time{}, "")
}

// Release 释放当前租约，持久保存下一次允许执行的时间。
func (s *Store) Release(ctx context.Context, reservation *queue.Reservation, at time.Time) error {
	return s.transition(ctx, reservation, "release", at, "")
}

// Fail 将当前领取任务保存在失败集合，等待人工 Retry。
func (s *Store) Fail(ctx context.Context, reservation *queue.Reservation, reason string, at time.Time) error {
	if len(reason) > 128 {
		return errors.New("redis queue failure reason must not exceed 128 bytes")
	}
	return s.transition(ctx, reservation, "fail", at, reason)
}

func (s *Store) transition(ctx context.Context, r *queue.Reservation, action string, at time.Time, reason string) error {
	if r == nil || r.Task == nil || strings.TrimSpace(r.Task.ID) == "" || len(r.Task.ID) > 128 || r.Token == "" {
		return queue.ErrLeaseLost
	}
	timestamp := at.UnixMilli()
	if action == "release" {
		timestamp = scheduledMillis(at)
	}
	result, err := s.client.Eval(ctx, transitionScript, s.keys, r.Task.ID, r.Token, action, timestamp, reason, at.Format(time.RFC3339Nano)).Int()
	if err != nil {
		return fmt.Errorf("%s redis task: %w", action, err)
	}
	if result == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

// Failed 返回按失败时间排序的独立副本；limit 必须为正数。
func (s *Store) Failed(ctx context.Context, limit int) ([]queue.FailedTask, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("redis queue failed limit must be between 1 and 1000")
	}
	values, err := s.client.Eval(ctx, failedScript, s.keys, limit).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("list failed redis tasks: %w", err)
	}
	result := make([]queue.FailedTask, 0, len(values))
	for _, raw := range values {
		var value record
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("decode failed redis record: %w", err)
		}
		task := &queue.Task{}
		if err := json.Unmarshal([]byte(value.Task), task); err != nil {
			return nil, fmt.Errorf("decode failed redis task: %w", err)
		}
		result = append(result, queue.FailedTask{Task: task, Attempts: value.Attempts, Reason: value.Reason, FailedAt: time.UnixMilli(value.FailedAt)})
	}
	return result, nil
}

// Retry 将失败任务重新排期，并重置领取次数。
func (s *Store) Retry(ctx context.Context, id string, at time.Time) error {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return queue.ErrNotFound
	}
	result, err := s.client.Eval(ctx, retryScript, s.keys, id, scheduledMillis(at), at.Format(time.RFC3339Nano)).Int()
	if err != nil {
		return fmt.Errorf("retry redis task: %w", err)
	}
	if result == 0 {
		return queue.ErrNotFound
	}
	return nil
}

// scheduledMillis 将截止时间向上取整；与向下取整的查询时钟比较，避免提前执行或回收租约。
func scheduledMillis(at time.Time) int64 {
	value := at.UnixMilli()
	if at.Nanosecond()%int(time.Millisecond) != 0 {
		value++
	}
	return value
}
