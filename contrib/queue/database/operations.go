package database

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// OperationsRepo 是 Repo 可选实现的任务查询与终态维护契约。
// 每个变更必须在存储层原子校验实际状态与租约。
type OperationsRepo interface {
	ListTasks(context.Context, queue.TaskQuery) (queue.TaskPage, error)
	GetTask(context.Context, string) (queue.TaskSnapshot, error)
	DeleteTask(context.Context, string) error
	CancelTask(context.Context, string) error
	RetryTask(context.Context, string, time.Time) error
	CleanupTasks(context.Context, queue.CleanupOptions) (queue.CleanupResult, error)
}

type operations struct {
	repo OperationsRepo
}

// QueueOperations 仅在底层 Repo 实现 OperationsRepo 时暴露运维能力。
func (s *Store) QueueOperations() (queue.Operations, bool) {
	repo, ok := s.repo.(OperationsRepo)
	if !ok {
		return nil, false
	}
	return &operations{repo: repo}, true
}

func (o *operations) List(ctx context.Context, query queue.TaskQuery) (queue.TaskPage, error) {
	if err := query.Validate(); err != nil {
		return queue.TaskPage{}, err
	}
	page, err := o.repo.ListTasks(ctx, query)
	if err != nil {
		return queue.TaskPage{}, err
	}
	page.Tasks = slices.Clone(page.Tasks)
	return page, nil
}

func (o *operations) Get(ctx context.Context, id string) (queue.TaskSnapshot, error) {
	if !validOperationsID(id) {
		return queue.TaskSnapshot{}, queue.ErrNotFound
	}
	snapshot, err := o.repo.GetTask(ctx, id)
	if err != nil {
		return queue.TaskSnapshot{}, err
	}
	return snapshot.Clone(), nil
}

func (o *operations) Delete(ctx context.Context, id string) error {
	if !validOperationsID(id) {
		return queue.ErrNotFound
	}
	return o.repo.DeleteTask(ctx, id)
}

func (o *operations) Cancel(ctx context.Context, id string) error {
	if !validOperationsID(id) {
		return queue.ErrNotFound
	}
	return o.repo.CancelTask(ctx, id)
}

func (o *operations) Retry(ctx context.Context, id string, at time.Time) error {
	if !validOperationsID(id) {
		return queue.ErrNotFound
	}
	return o.repo.RetryTask(ctx, id, at)
}

func (o *operations) Cleanup(ctx context.Context, options queue.CleanupOptions) (queue.CleanupResult, error) {
	if err := options.Validate(); err != nil {
		return queue.CleanupResult{}, err
	}
	return o.repo.CleanupTasks(ctx, options)
}

func validOperationsID(id string) bool {
	return len(id) <= 128 && strings.TrimSpace(id) != ""
}

var _ queue.OperationsProvider = (*Store)(nil)
