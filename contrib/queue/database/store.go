// Package database 将业务实现的单队列 Repo 适配为任务 Store，不依赖 ORM 或数据库驱动。
package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// Store 校验并转换任务业务契约，数据持久化和原子状态转换由 Repo 实现。
// Store 不拥有 Repo/连接，没有表名配置、迁移入口或 cleanup。
type Store struct{ repo Repo }

// NewStore 适配已经绑定单个队列的 Repo，构造阶段不调用 Repo。
func NewStore(repo Repo) *Store { return &Store{repo: repo} }

// Enqueue 保存独立任务快照，重复 ID 由 Repo 返回 queue.ErrDuplicate。
func (s *Store) Enqueue(ctx context.Context, task *queue.Task) error {
	if task == nil || strings.TrimSpace(task.ID) == "" || len(task.ID) > 128 || strings.TrimSpace(task.Type) == "" {
		return errors.New("database queue requires task id (1 to 128 bytes) and type")
	}
	return s.repo.Insert(ctx, &TaskRecord{Task: *task.Clone()})
}

// Reserve 通过 Repo 原子领取任务，使用随机 token 隔离每次领取。
func (s *Store) Reserve(ctx context.Context, now time.Time, lease time.Duration) (*queue.Reservation, error) {
	if lease < time.Millisecond {
		return nil, errors.New("database queue lease must be at least one millisecond")
	}
	token := uuid.NewString()
	until := now.Add(lease)
	record, err := s.repo.Claim(ctx, now, until, token)
	if err != nil || record == nil {
		return nil, err
	}
	// Repo 是可替换边界；无效领取不能进入 Handler，也不能用不可信 token 改写其他记录。
	if record.Token != token || record.Failed || record.ReservedUntil.Before(until) || record.Attempts < 1 || strings.TrimSpace(record.Task.ID) == "" || len(record.Task.ID) > 128 || strings.TrimSpace(record.Task.Type) == "" || record.Task.AvailableAt.After(now) {
		return nil, errors.New("database queue repo returned an invalid claim")
	}
	return &queue.Reservation{Task: record.Task.Clone(), Token: token, Attempts: record.Attempts}, nil
}

func validReservation(r *queue.Reservation) bool {
	return r != nil && r.Task != nil && strings.TrimSpace(r.Task.ID) != "" && len(r.Task.ID) <= 128 && r.Token != ""
}

// Ack 请求 Repo 按 token 删除当前任务。
func (s *Store) Ack(ctx context.Context, r *queue.Reservation) error {
	if !validReservation(r) {
		return queue.ErrLeaseLost
	}
	return s.repo.DeleteReserved(ctx, r.Task.ID, r.Token)
}

// Release 请求 Repo 保留次数并保存下次执行时间。
func (s *Store) Release(ctx context.Context, r *queue.Reservation, at time.Time) error {
	if !validReservation(r) {
		return queue.ErrLeaseLost
	}
	return s.repo.ReleaseReserved(ctx, r.Task.ID, r.Token, at)
}

// Fail 请求 Repo 保留任务并保存受控失败分类。
func (s *Store) Fail(ctx context.Context, r *queue.Reservation, reason string, at time.Time) error {
	if len(reason) > 128 {
		return errors.New("database queue failure reason exceeds 128 bytes")
	}
	if !validReservation(r) {
		return queue.ErrLeaseLost
	}
	return s.repo.FailReserved(ctx, r.Task.ID, r.Token, reason, at)
}

// Failed 返回独立的失败任务快照；limit 必须在 1..1000 内。
func (s *Store) Failed(ctx context.Context, limit int) ([]queue.FailedTask, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("database queue failed limit must be between 1 and 1000")
	}
	records, err := s.repo.ListFailed(ctx, limit)
	if err != nil {
		return nil, err
	}
	if len(records) > limit {
		return nil, errors.New("database queue repo exceeded failed limit")
	}
	result := make([]queue.FailedTask, 0, len(records))
	for _, record := range records {
		if !record.Failed || strings.TrimSpace(record.Task.ID) == "" || strings.TrimSpace(record.Task.Type) == "" {
			return nil, errors.New("database queue repo returned an invalid failure")
		}
		result = append(result, queue.FailedTask{Task: record.Task.Clone(), Attempts: record.Attempts, Reason: record.FailureReason, FailedAt: record.FailedAt})
	}
	return result, nil
}

// Retry 请求 Repo 清零失败任务次数并重新排期。
func (s *Store) Retry(ctx context.Context, id string, at time.Time) error {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return queue.ErrNotFound
	}
	return s.repo.RetryFailed(ctx, id, at)
}

var _ queue.Store = (*Store)(nil)
